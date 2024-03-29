package datastore

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"compress/gzip"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type HeaderReader interface {
	io.ReadCloser
}

// TODO: May want to add metadata as well, including source URL and cache timestamp.
type ResourceReader interface {
	io.ReadCloser
	Headers() *http.Header
	ResourceURL() string
}

type ResourceWriter interface {
	io.WriteCloser

	// WriteHeaders must be called before Write, otherwise headers will be
	// assumed to be empty.
	WriteHeaders(headers *http.Header) error
}

type ResourceMetadata struct {
	Url              string
	DownloadStarted  time.Time
	DownloadDuration time.Duration
	RawBytes         int
	BytesOnDisk      int
}

type ResourceIterator interface {
	Next() (ResourceMetadata, error)
	HasNext() bool
}

type ResourceStats struct {
	RecordCount          int64
	DiskConsumptionBytes int
}

type ResourceStatus int

const (
	ResourceNotCached ResourceStatus = iota
	ResourceDownloading
	ResourceCached
)

type Datastore interface {
	Status(hashedUrl string) (ResourceStatus, error)

	// Resource must exist when this method is called.
	// If the resource is in the process of downloading, blocks until it is finished downloading.
	Open(hashedUrl string) (ResourceReader, error)

	// Creates resource if it does not exist.
	// Returns (nil, nil) if the resource already exists.
	TryCreate(resourceURL string, hashedUrl string) (ResourceWriter, error)

	List(offset, count int) (ResourceIterator, error)

	Stats() (ResourceStats, error)
	// TODO: Might need to add Close method here as well once we add a networked
	// db.

}

type resourceMetadata struct {
	gorm.Model

	// Hashed URL
	HashedUrl string `gorm:"unique"`

	// Original URL.
	Url string `gorm:"unique"`

	// Request Headers.
	RequestHeaders string

	// Response Headers
	ResponseHeaders string

	// Time download initiated.
	DownloadStarted time.Time

	// Time download finished.
	DownloadFinished time.Time

	// Number of bytes in the body of the resource uncompressed.
	RawBytes int

	// Number of bytes in the body of the resource as stored on disk.
	BytesOnDisk int

	// Whether the download has finished yet.
	DownloadComplete bool
}

func resourceFilepath(rootPath string, resourceId uint) string {
	return rootPath + strconv.FormatUint(uint64(resourceId), 10)
}

func splitHeaderPair(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	if i := bytes.Index(data, []byte(":")); i >= 0 {
		return i + 1, data[:i], nil
	}

	if atEOF {
		return len(data), data, nil
	}

	return
}

type headerParseError struct {
	msg string
}

func (e headerParseError) Error() string {
	return e.msg
}

type FileResourceReader struct {
	g           io.ReadCloser // gzip Reader
	resourceURL string
	// TODO: Change name to response headers
	headers *http.Header
}

func newFileResourceReader(f *os.File, resourceURL string, headers *http.Header) (FileResourceReader, error) {
	g, err := gzip.NewReader(f)
	if err != nil {
		return FileResourceReader{}, err
	}
	return FileResourceReader{g, resourceURL, headers}, nil
}

func (rr FileResourceReader) Read(b []byte) (int, error) {
	return rr.g.Read(b)
}

func (rr FileResourceReader) Close() error {
	return rr.g.Close()
}

func (rr FileResourceReader) Headers() *http.Header {
	return rr.headers
}

func (rr FileResourceReader) ResourceURL() string {
	return rr.resourceURL
}

type FileResourceWriter struct {
	g        io.WriteCloser // gzip writer
	headers  *http.Header
	id       uint
	ds       *FileDatastore
	rawBytes int
}

func headersAsString(headers *http.Header) (string, error) {
	if headers == nil {
		return "", nil
	}
	var headersBuffer bytes.Buffer
	if err := headers.Write(&headersBuffer); err != nil {
		return "", err
	}
	// Need an extra CRLF to mark the end of the headers.
	if _, err := headersBuffer.Write([]byte("\r\n")); err != nil {
		return "", err
	}
	return headersBuffer.String(), nil
}

func (rw *FileResourceWriter) Write(b []byte) (int, error) {
	rawBytes, err := rw.g.Write(b)
	rw.rawBytes += rawBytes
	return rawBytes, err
}

func (rw *FileResourceWriter) writeFinalMetadata() error {
	fi, err := os.Stat(resourceFilepath(rw.ds.rootPath, rw.id))
	if err != nil {
		return err
	}
	bytesOnDisk := fi.Size()
	responseHeaders, err := headersAsString(rw.headers)
	if err != nil {
		return err
	}
	rm := resourceMetadata{}
	result := rw.ds.db.Model(&rm).Where("id = ?", rw.id).Updates(map[string]interface{}{
		"response_headers":  responseHeaders,
		"download_finished": time.Now(),
		"raw_bytes":         rw.rawBytes,
		"bytes_on_disk":     bytesOnDisk,
		"download_complete": true,
	})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func (rw *FileResourceWriter) Close() error {
	if err := rw.g.Close(); err != nil {
		return err
	}
	if err := rw.writeFinalMetadata(); err != nil {
		return err
	}
	return nil
}

func (rw *FileResourceWriter) WriteHeaders(headers *http.Header) error {
	rw.headers = headers
	return nil
}

func newFileResourceWriter(f *os.File, id uint, ds *FileDatastore) (*FileResourceWriter, error) {
	return &FileResourceWriter{gzip.NewWriter(f), nil, id, ds, 0}, nil
}

type FileDatastore struct {
	rootPath string
	db       *gorm.DB
}

func NewFileDatastore(dbFilePath string, rootPath string) (FileDatastore, error) {
	// Must end in a slash.
	if rootPath != "" && !strings.HasSuffix(rootPath, "/") {
		rootPath += "/"
	}
	// TODO: Check if it exists first.
	db, err := gorm.Open(sqlite.Open(dbFilePath), &gorm.Config{})
	if err != nil {
		return FileDatastore{}, err
	}
	if err = db.AutoMigrate(&resourceMetadata{}); err != nil {
		return FileDatastore{}, err
	}
	return FileDatastore{rootPath, db}, nil
}

func (ds FileDatastore) Status(hashedUrl string) (ResourceStatus, error) {
	rm := resourceMetadata{}
	result := ds.db.First(&rm, "hashed_url = ?", hashedUrl)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return ResourceNotCached, nil
	} else if result.Error != nil {
		return ResourceNotCached, result.Error
	} else if !rm.DownloadComplete {
		return ResourceDownloading, nil
	} else {
		return ResourceCached, nil
	}
}

func readHeaders(hs string) (*http.Header, error) {
	headerBuffer := bytes.NewBufferString(hs)
	headers := make(http.Header)
	scanner := bufio.NewScanner(headerBuffer)
	for scanner.Scan() {
		pairBytes := scanner.Bytes()
		if len(pairBytes) == 0 {
			// Empty line.
			break
		}
		pairScanner := bufio.NewScanner(bytes.NewReader(pairBytes))
		pairScanner.Split(splitHeaderPair)
		if !pairScanner.Scan() {
			return nil, headerParseError{fmt.Sprintf("Did not find a colon in header pair: %s", string(pairBytes))}
		}
		rawKey := pairScanner.Text()
		key := strings.TrimRight(rawKey, " \t")
		value := strings.TrimLeft(string(pairBytes[len(rawKey)+1:]), " \t")
		currentValues, ok := headers[key]
		if !ok {
			headers[key] = []string{value}
		} else {
			headers[key] = append(currentValues, value)
		}
	}
	return &headers, nil
}

type successFunc func() error

func withExponentialBackoff(f successFunc, base time.Duration, growthFactor float64, maxDuration time.Duration, maxTime time.Duration) error {
	tries := 0
	currentDelay := base
	totalTime := 0 * time.Second
	for {
		err := f()
		if err == nil {
			return nil
		}
		tries += 1
		if totalTime >= maxTime {
			return fmt.Errorf("Exceeded maximum timeout of %v: %v", maxTime, err)
		}
		log.Printf("%v\n  Attempt %d failed. Trying again in %v.", err, tries+1, currentDelay)
		time.Sleep(currentDelay)
		totalTime += currentDelay
		currentDelay = time.Duration(int64(math.Round(growthFactor * float64(currentDelay.Nanoseconds()))))
		if currentDelay >= maxDuration {
			currentDelay = maxDuration
		}
	}

	return fmt.Errorf("Unreachable code.")
}

func (ds FileDatastore) awaitCompletedResource(hashedUrl string) (resourceMetadata, error) {
	rm := resourceMetadata{}
	getResource := func() error {
		result := ds.db.First(&rm, "hashed_url = ?", hashedUrl)
		if result.Error != nil {
			return result.Error
		}
		if !rm.DownloadComplete {
			return fmt.Errorf("download incomplete")
		}
		return nil
	}
	err := withExponentialBackoff(getResource,
		100*time.Millisecond,
		1.5,
		10*time.Second,
		30*time.Minute)
	if err != nil {
		return rm, err
	}
	return rm, nil
}

func (ds FileDatastore) Open(hashedUrl string) (ResourceReader, error) {
	rm, err := ds.awaitCompletedResource(hashedUrl)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(ds.rootPath + strconv.FormatUint(uint64(rm.ID), 10))
	if err != nil {
		return nil, err
	}
	headers, err := readHeaders(rm.ResponseHeaders)
	if err != nil {
		return nil, err
	}
	return newFileResourceReader(f, rm.Url, headers)
}

func (ds FileDatastore) tryCreateStubRecord(resourceUrl, hashedUrl string) (bool, uint, error) {
	// TODO: Actually collect requestHeaders
	rm := &resourceMetadata{
		gorm.Model{},
		hashedUrl,
		resourceUrl,
		"",
		"",
		time.Now(),
		time.UnixMicro(0),
		0,
		0,
		false,
	}
	result := ds.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&rm)

	if result.Error != nil {
		return false, 0, result.Error
	}
	if result.RowsAffected == 0 {
		return false, 0, nil
	}
	return true, rm.ID, nil
}

func (ds FileDatastore) TryCreate(resourceURL string, hashedUrl string) (ResourceWriter, error) {
	created, id, err := ds.tryCreateStubRecord(resourceURL, hashedUrl)
	if err != nil {
		return nil, err
	}

	if !created {
		return nil, nil
	}

	f, err := os.Create(resourceFilepath(ds.rootPath, id))
	if err != nil {
		return nil, err
	}
	fileResourceWriter, err := newFileResourceWriter(f, id, &ds)
	if err != nil {
		return nil, err
	}
	var resourceWriter ResourceWriter = fileResourceWriter
	return resourceWriter, nil
}

type fileResourceIterator struct {
	rootPath string
	rms      *[]resourceMetadata
	index    int
}

func (fri *fileResourceIterator) Next() (ResourceMetadata, error) {
	rm := (*fri.rms)[fri.index]
	fri.index += 1
	return ResourceMetadata{rm.Url, rm.DownloadStarted, rm.DownloadFinished.Sub(rm.DownloadStarted), rm.RawBytes, rm.BytesOnDisk}, nil
}

func (fri *fileResourceIterator) HasNext() bool {
	return fri.index < len(*fri.rms)
}

func (ds FileDatastore) List(offset, count int) (ResourceIterator, error) {
	var rms []resourceMetadata
	result := ds.db.Limit(count).Offset(offset).Order("download_started desc").Find(&rms)
	if result.Error != nil {
		return nil, result.Error
	}
	return &fileResourceIterator{ds.rootPath, &rms, 0}, nil
}

func (ds FileDatastore) Stats() (ResourceStats, error) {
	var resourceCount int64 = 0
	ds.db.Model(&resourceMetadata{}).Count(&resourceCount)

	var byteSum int = 0
	ds.db.Model(&resourceMetadata{}).Select("sum(bytes_on_disk)").Scan(&byteSum)

	return ResourceStats{resourceCount, byteSum}, nil
}

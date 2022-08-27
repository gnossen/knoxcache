package datastore

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"compress/gzip"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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

type Datastore interface {
	Exists(hashedUrl string) (bool, error)

	// Resource must exist when this method is called.
	Open(hashedUrl string) (ResourceReader, error)

	// Resource must not exist when this method is called.
	Create(resourceURL string, hashedUrl string) (ResourceWriter, error)

	List(offset, count int) (ResourceIterator, error)
	// TODO: Might need to add Close method here as well once we add a networked
	// db.

}

type resourceMetadata struct {
	gorm.Model

	// Hashed URL
	HashedUrl string

	// Original URL.
	Url string

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

func (ds FileDatastore) Exists(hashedUrl string) (bool, error) {
	rm := resourceMetadata{}
	result := ds.db.First(&rm, "hashed_url = ?", hashedUrl)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return false, nil
	} else if result.Error != nil {
		return false, result.Error
	} else {
		return true, nil
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

func (ds FileDatastore) Open(hashedUrl string) (ResourceReader, error) {
	rm := resourceMetadata{}
	result := ds.db.First(&rm, "hashed_url = ?", hashedUrl)
	if result.Error != nil {
		return nil, result.Error
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

func (ds FileDatastore) createStubRecord(resourceUrl, hashedUrl string) (uint, error) {
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
	}
	result := ds.db.Create(&rm)
	if result.Error != nil {
		return 0, result.Error
	}
	return rm.ID, nil
}

func (ds FileDatastore) Create(resourceURL string, hashedUrl string) (ResourceWriter, error) {
	id, err := ds.createStubRecord(resourceURL, hashedUrl)
	if err != nil {
		return nil, err
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

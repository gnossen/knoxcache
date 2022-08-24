package datastore

import (
	"bufio"
	"bytes"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const sqliteFilename = "knox.db"

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
	Url          string
	CreationTime time.Time
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

	// TODO: Add pagination.
	List() (ResourceIterator, error)
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
	f           *os.File
	resourceURL string
	// TODO: Change name to response headers
	headers *http.Header
}

func newFileResourceReader(f *os.File, resourceURL string, headers *http.Header) (FileResourceReader, error) {
	return FileResourceReader{f, resourceURL, headers}, nil
}

func (rr FileResourceReader) Read(b []byte) (int, error) {
	return rr.f.Read(b)
}

func (rr FileResourceReader) Close() error {
	return rr.f.Close()
}

func (rr FileResourceReader) Headers() *http.Header {
	return rr.headers
}

func (rr FileResourceReader) ResourceURL() string {
	return rr.resourceURL
}

type FileResourceWriter struct {
	f       *os.File
	headers *http.Header
	id      uint
	ds      *FileDatastore
}

func writeUint64(output uint64, w io.Writer) (int, error) {
	uint64Buffer := make([]byte, 8)
	binary.LittleEndian.PutUint64(uint64Buffer, output)
	lengthWritten, err := io.Copy(w, bytes.NewReader(uint64Buffer))
	if err != nil {
		return int(lengthWritten), err
	}
	if lengthWritten != 8 {
		return int(lengthWritten), fmt.Errorf("Expected to write %d bytes but wrote %d.", 8, lengthWritten)
	}
	return 8, nil
}

func writeLengthPrefixedString(s string, w io.Writer) (int, error) {
	lenWritten := 0
	n, err := writeUint64(uint64(len(s)), w)
	lenWritten += n
	if err != nil {
		return lenWritten, fmt.Errorf("failed to write string prefix length of '%s': %v", s, err)
	}

	n, err = io.WriteString(w, s)
	lenWritten += n
	if err != nil {
		return lenWritten, fmt.Errorf("failed to write string '%s': %v", s, err)
	}

	return lenWritten, nil
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
	bodyLen, err := rw.f.Write(b)
	return bodyLen, err
}

func (rw *FileResourceWriter) writeFinalMetadata() error {
	responseHeaders, err := headersAsString(rw.headers)
	if err != nil {
		return err
	}
	rm := resourceMetadata{}
	result := rw.ds.db.Model(&rm).Where("id = ?", rw.id).Updates(map[string]interface{}{
		"response_headers":  responseHeaders,
		"download_finished": time.Now(),
	})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func (rw *FileResourceWriter) Close() error {
	if err := rw.writeFinalMetadata(); err != nil {
		return err
	}
	return rw.f.Close()
}

func (rw *FileResourceWriter) WriteHeaders(headers *http.Header) error {
	rw.headers = headers
	return nil
}

func newFileResourceWriter(f *os.File, id uint, ds *FileDatastore) (*FileResourceWriter, error) {
	return &FileResourceWriter{f, nil, id, ds}, nil
}

type FileDatastore struct {
	rootPath string
	db       *gorm.DB
}

func NewFileDatastore(rootPath string) (FileDatastore, error) {
	// Must end in a slash.
	if rootPath != "" && !strings.HasSuffix(rootPath, "/") {
		rootPath += "/"
	}
	// TODO: Check if it exists first.
	db, err := gorm.Open(sqlite.Open(rootPath+sqliteFilename), &gorm.Config{})
	if err != nil {
		return FileDatastore{}, err
	}
	if err = db.AutoMigrate(&resourceMetadata{}); err != nil {
		return FileDatastore{}, err
	}
	return FileDatastore{rootPath, db}, nil
}

// TODO: Filesystems have a hard limit on length of filenames. Need to shorten
// by, e.g. hashing.
func (ds FileDatastore) translateUrlToFilePath(hashedUrl string) string {
	h := sha512.New()
	h.Write([]byte(hashedUrl))
	fileName := hex.EncodeToString(h.Sum(nil))
	return ds.rootPath + fileName
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

	f, err := os.Create(ds.rootPath + strconv.FormatUint(uint64(id), 10))
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
	rootPath   string
	dirEntries []os.DirEntry
	index      int
}

func (fri *fileResourceIterator) Next() (ResourceMetadata, error) {
	panic("TODO: Implement")
	fri.index += 1
	return ResourceMetadata{}, nil
}

func (fri *fileResourceIterator) HasNext() bool {
	// TODO: Filter out directories?
	return fri.index < len(fri.dirEntries)
}

func (ds FileDatastore) List() (ResourceIterator, error) {
	files, err := os.ReadDir(ds.rootPath)
	if err != nil {
		return &fileResourceIterator{}, fmt.Errorf("failed to list files in %s", ds.rootPath)
	}
	return &fileResourceIterator{ds.rootPath, files, 0}, nil
}

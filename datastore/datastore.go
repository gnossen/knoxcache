package datastore

import (
	"bufio"
	"bytes"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

    _ "gorm.io/gorm"
    _ "gorm.io/driver/sqlite"
)

const currentFileResourceReaderVersion = 2

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

	List() (ResourceIterator, error)
	// TODO: Might need to add Close method here as well once we add a networked
	// db.

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

// TODO: Modify to use our own buffer instead of bufio so that we can guarantee
// that we only read up to n characters.
// Reads headers in wire format exactly of length n into the header.
func readHeaders(reader io.Reader) (*http.Header, error) {
	headers := make(http.Header)
	scanner := bufio.NewScanner(reader)
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

type FileResourceReader struct {
	f           *os.File
	resourceURL string
	headers     *http.Header
}

func readUint64(f io.Reader) (uint64, error) {
	buf := []byte{0, 0, 0, 0, 0, 0, 0, 0}
	n, err := f.Read(buf)
	if err != nil {
		return 0, err
	}
	if n != len(buf) {
		return 0, fmt.Errorf("Attempted to read 8 bytes, but only found %d.", n)
	}
	return binary.LittleEndian.Uint64(buf), nil
}

// Reads a string prefixed by its own length, as a uint64.
func readLengthPrefixedString(f io.Reader) (string, error) {
	strLen, err := readUint64(f)
	if err != nil {
		return "", fmt.Errorf("failed to read string length: %v", err)
	}
	buf := new(strings.Builder)
	_, err = io.CopyN(buf, f, int64(strLen))
	if err != nil {
		return "", fmt.Errorf("failed to read string: %v", err)
	}
	return buf.String(), nil
}

func newFileResourceReader(f *os.File) (FileResourceReader, error) {
	var preambleLength uint64 = 0
	// TODO: Have readUint64 return its length.
	fileFormatVersion, err := readUint64(f)
	if err != nil {
		return FileResourceReader{nil, "", &http.Header{}}, fmt.Errorf("%s: Failed to read file format version: %v", f.Name(), err)
	}
	preambleLength += 8

	if fileFormatVersion != currentFileResourceReaderVersion {
		return FileResourceReader{nil, "", &http.Header{}}, fmt.Errorf("%s: Unsupported file format version: %d. Supported versions: [%d]", f.Name(), fileFormatVersion, currentFileResourceReaderVersion)
	}

	// TODO: Have readLengthPrefixedString return its length.
	resourceURL, err := readLengthPrefixedString(f)
	if err != nil {
		return FileResourceReader{nil, "", &http.Header{}}, fmt.Errorf("%s: Failed to read resource URL: %v", f.Name(), err)
	}

	preambleLength += 8
	preambleLength += uint64(len(resourceURL))

	headerLength, err := readUint64(f)
	if err != nil {
		return FileResourceReader{nil, "", &http.Header{}}, fmt.Errorf("%s: Failed to read header length: %v", f.Name(), err)
	}

	preambleLength += 8
	preambleLength += headerLength

	// Read headers
	var headers *http.Header
	if headerLength != 0 {
		headers, err = readHeaders(f)
	} else {
		headers = &http.Header{}
	}
	if err != nil {
		return FileResourceReader{nil, "", &http.Header{}}, err
	}

	// Seek to beginning of content.
	_, err = f.Seek(int64(preambleLength), 0)
	if err != nil {
		return FileResourceReader{nil, "", &http.Header{}}, err
	}
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
	f               *os.File
	headers         *http.Header
	resourceURL     string
	preambleWritten bool
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

func (rw *FileResourceWriter) writeHeaders() (int, error) {
	var headersBuffer bytes.Buffer
	if rw.headers != nil {
		if err := rw.headers.Write(&headersBuffer); err != nil {
			return 0, err
		}
		// Need an extra CRLF to mark the end of the headers.
		if _, err := headersBuffer.Write([]byte("\r\n")); err != nil {
			return 0, err
		}
	}
	totalWritten := 0

	headerLength := uint64(headersBuffer.Len())
	headerLengthWritten, err := writeUint64(headerLength, rw.f)
	totalWritten += headerLengthWritten
	if err != nil {
		return totalWritten, fmt.Errorf("Failed to write length of HTTP headers: %v", err)
	}

	headersBodyLengthWritten, err := io.Copy(rw.f, &headersBuffer)
	totalWritten += int(headersBodyLengthWritten)
	if err != nil {
		return totalWritten, err
	}
	if uint64(headersBodyLengthWritten) != headerLength {
		return totalWritten, fmt.Errorf("Failed to write headers. Expected to write %d bytes but wrote %d.", 8, headerLength, headersBodyLengthWritten)
	}
	return totalWritten, nil
}

func (rw *FileResourceWriter) writePreamble() (int, error) {
	lenWritten := 0
	n, err := writeUint64(uint64(currentFileResourceReaderVersion), rw.f)
	if err != nil {
		return lenWritten, fmt.Errorf("failed to write file format version: %v", err)
	}
	lenWritten += n
	n, err = writeLengthPrefixedString(rw.resourceURL, rw.f)
	lenWritten += n
	if err != nil {
		return lenWritten, fmt.Errorf("failed to write url '%s': %v", rw.resourceURL, err)
	}
	headerLen, err := rw.writeHeaders()
	lenWritten += headerLen
	if err != nil {
		return lenWritten, err
	}
	rw.preambleWritten = true
	return lenWritten, nil
}

func (rw *FileResourceWriter) Write(b []byte) (int, error) {
	var preambleLen int
	if !rw.preambleWritten {
		preambleLen, err := rw.writePreamble()
		if err != nil {
			return preambleLen, fmt.Errorf("Failed to write preamble: %v", err)
		}
	}
	bodyLen, err := rw.f.Write(b)
	return preambleLen + bodyLen, err
}

func (rw *FileResourceWriter) Close() error {
	return rw.f.Close()
}

func (rw *FileResourceWriter) WriteHeaders(headers *http.Header) error {
	rw.headers = headers
	return nil
}

func newFileResourceWriter(f *os.File, resourceURL string) (*FileResourceWriter, error) {
	return &FileResourceWriter{f, nil, resourceURL, false}, nil
}

type FileDatastore struct {
	rootPath string
}

func NewFileDatastore(rootPath string) FileDatastore {
	// Must end in a slash.
	if rootPath != "" && !strings.HasSuffix(rootPath, "/") {
		rootPath += "/"
	}
	// TODO: Check if it exists first.
	return FileDatastore{rootPath}
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
	filepath := ds.translateUrlToFilePath(hashedUrl)
	_, err := os.Stat(filepath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (ds FileDatastore) Open(hashedUrl string) (ResourceReader, error) {
	f, err := os.Open(ds.translateUrlToFilePath(hashedUrl))
	if err != nil {
		return nil, err
	}
	return newFileResourceReader(f)
}

func (ds FileDatastore) Create(resourceURL string, hashedUrl string) (ResourceWriter, error) {
	f, err := os.Create(ds.translateUrlToFilePath(hashedUrl))
	if err != nil {
		return nil, err
	}
	fileResourceWriter, err := newFileResourceWriter(f, resourceURL)
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
	// TODO: Filter out directories?
	filename := fri.dirEntries[fri.index].Name()
	fri.index += 1

	filePath := fri.rootPath + filename

	var creationTime time.Time
	fi, err := os.Stat(filePath)
	if err != nil {
		creationTime = time.UnixMilli(0)
	} else {
		stat := fi.Sys().(*syscall.Stat_t)
		creationTime = time.Unix(int64(stat.Ctim.Sec), int64(stat.Ctim.Nsec))
	}

	f, err := os.Open(filePath)
	if err != nil {
		return ResourceMetadata{}, fmt.Errorf("failed to open %s", filename)
	}
	rr, err := newFileResourceReader(f)
	defer rr.Close()
	if err != nil {
		return ResourceMetadata{}, err
	}

	return ResourceMetadata{rr.ResourceURL(), creationTime}, nil
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

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
)

type HeaderReader interface {
	io.ReadCloser
}

// TODO: May want to add metadata as well, including source URL and cache timestamp.
type ResourceReader interface {
	io.ReadCloser
	Headers() *http.Header
}

type ResourceWriter interface {
	io.WriteCloser

	// WriteHeaders must be called before Write, otherwise headers will be
	// assumed to be empty.
	WriteHeaders(headers *http.Header) error
}

type Datastore interface {
	Exists(hashedUrl string) (bool, error)

	// Resource must exist when this method is called.
	Open(hashedUrl string) (ResourceReader, error)

	// Resource must not exist when this method is called.
	Create(hashedUrl string) (ResourceWriter, error)

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
	f       *os.File
	headers *http.Header
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

func newFileResourceReader(f *os.File) (FileResourceReader, error) {
	fileFormatVersion, err := readUint64(f)
	if err != nil {
		return FileResourceReader{nil, &http.Header{}}, fmt.Errorf("%s: Failed to read file format version: %v", f.Name(), err)
	}

	if fileFormatVersion != 0 {
		return FileResourceReader{nil, &http.Header{}}, fmt.Errorf("%s: Unsupported file format version: %d. Supported versions: [0]", f.Name(), fileFormatVersion)
	}

	headerLength, err := readUint64(f)
	if err != nil {
		return FileResourceReader{nil, &http.Header{}}, fmt.Errorf("%s: Failed to read header length: %v", f.Name(), err)
	}

	// Read headers
	var headers *http.Header
	if headerLength != 0 {
		headers, err = readHeaders(f)
	}
	if err != nil {
		return FileResourceReader{nil, &http.Header{}}, err
	}

	// Seek to beginning of content.
	_, err = f.Seek(int64(headerLength)+8, 0)
	if err != nil {
		return FileResourceReader{nil, &http.Header{}}, err
	}
	return FileResourceReader{f, headers}, nil
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

type FileResourceWriter struct {
	f              *os.File
	headers        *http.Header
	headersWritten bool
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

	versionLengthWritten, err := writeUint64(0, rw.f)
	totalWritten += versionLengthWritten
	if err != nil {
		return totalWritten, fmt.Errorf("Failed to write file version: %v", err)
	}

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

func (rw *FileResourceWriter) Write(b []byte) (int, error) {
	var headerLen int
	if !rw.headersWritten {
		headerLen, err := rw.writeHeaders()
		if err != nil {
			return headerLen, err
		}
		rw.headersWritten = true
	}
	bodyLen, err := rw.f.Write(b)
	return headerLen + bodyLen, err
}

func (rw *FileResourceWriter) Close() error {
	return rw.f.Close()
}

func (rw *FileResourceWriter) WriteHeaders(headers *http.Header) error {
	rw.headers = headers
	return nil
}

func newFileResourceWriter(f *os.File) (*FileResourceWriter, error) {
	return &FileResourceWriter{f, nil, false}, nil
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

func (ds FileDatastore) Create(hashedUrl string) (ResourceWriter, error) {
	f, err := os.Create(ds.translateUrlToFilePath(hashedUrl))
	if err != nil {
		return nil, err
	}
	fileResourceWriter, err := newFileResourceWriter(f)
	if err != nil {
		return nil, err
	}
	var resourceWriter ResourceWriter = fileResourceWriter
	return resourceWriter, nil
}

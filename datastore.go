package datastore

import (
    "bytes"
    "io"
    "os"
    "strings"
    "net/http"
    "fmt"
    "bufio"
    "encoding/binary"
)

type HeaderReader interface {
    io.ReadCloser
}

type ResourceReader interface {
    io.ReadCloser
    Headers() *http.Header
}

type Datastore interface {
    Exists(hashedUrl string) (bool, error)

    // Resource must exist when this method is called.
    Open(hashedUrl string) (ResourceReader, error)

    // Resource must not exist when this method is called.
    // TODO: May want to add metadata as well, including source URL and cache timestamp.
    Create(hashedUrl string) (io.WriteCloser, error)

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
        var pairElems [2]string
        for i := 0; i < 2; i++ {
           if !pairScanner.Scan() {
               return nil, headerParseError{fmt.Sprintf("Did not find a colon in header pair: %s", string(pairBytes))}
           }
           pairElems[i] = pairScanner.Text()
        }
        if pairScanner.Scan() {
            return nil, headerParseError{fmt.Sprintf("Too many colons in header pair: %s", string(pairBytes))}
        }
        key := strings.TrimRight(pairElems[0], " \t")
        value := strings.TrimLeft(pairElems[1], " \t")
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
    f *os.File
    headers *http.Header
}

type fileHeaderError struct {
    filename string
}

func (e fileHeaderError) Error() string {
    return fmt.Sprintf("File %s did not have mandatory 8 byte header. Perhaps it was truncated?", e.filename)
}

func newFileResourceReader(f *os.File) (FileResourceReader, error) {
    // Read 8-byte header length.
    buf := []byte{0, 0, 0, 0, 0, 0, 0}
    n, err := f.Read(buf)
    if err != nil {
        return FileResourceReader{nil, &http.Header{}}, err
    }
    if n != len(buf) {
        return FileResourceReader{nil, &http.Header{}}, fileHeaderError{f.Name()}
    }
    headerLength := binary.LittleEndian.Uint64(buf)

    // Read headers
    headers, err := readHeaders(f)
    if err != nil {
        return FileResourceReader{nil, &http.Header{}}, err
    }

    // Seek to beginning of content.
    _, err = f.Seek(int64(headerLength) + 8, 0)
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

func (ds FileDatastore) translateUrlToFilePath(hashedUrl string) string {
    return ds.rootPath + hashedUrl
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

func (ds FileDatastore) Create(hashedUrl string) (io.WriteCloser, error) {
    f, err := os.Create(ds.translateUrlToFilePath(hashedUrl))
    if err != nil {
        return nil, err
    }
    return f, nil
}

package datastore

import (
    "io"
    "os"
    "strings"
)

type Datastore interface {
    Exists(hashedUrl string) (bool, error)

    // Resource must exist when this method is called.
    Open(hashedUrl string) (io.ReadCloser, error)

    // Resource must not exist when this method is called.
    Create(hashedUrl string) (io.WriteCloser, error)

    // TODO: Might need to add Close method here as well once we add a networked
    // db.

}

type FileDatastore struct {
    rootPath string
}

func NewFileDatastore(rootPath string) FileDatastore {
    // Must end in a slash.
    if !strings.HasSuffix(rootPath, "/") {
       rootPath += "/"
    }
    // TODO: Check if it exists first.
    return FileDatastore{rootPath}
}

func (ds FileDatastore) translateUrlToFilePath(hashedUrl string) string {
    return ds.rootPath + hashedUrl
}

func (ds FileDatastore) Exists(hashedUrl string) (bool, error) {
    _, err := os.Stat(ds.translateUrlToFilePath(hashedUrl))
    if err != nil {
        return true, nil
    }
    if os.IsNotExist(err) {
        return false, nil
    }
    return false, err
}

func (ds FileDatastore) Open(hashedUrl string) (io.ReadCloser, error) {
    f, err := os.Open(ds.translateUrlToFilePath(hashedUrl))
    if err != nil {
        return nil, err
    }
    return f, nil
}

func (ds FileDatastore) Create(hashedUrl string) (io.WriteCloser, error) {
    f, err := os.Create(ds.translateUrlToFilePath(hashedUrl))
    if err != nil {
        return nil, err
    }
    return f, nil
}

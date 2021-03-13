package encoder

import (
    "crypto/sha256"
    "encoding/base32"
    "strings"
)

type Encoder interface {
    // Encodes a valid URL to a string of indeterminate length.
    Encode(url string) string

    // Decodes an encoded URL.
    // Is guaranteed to be an inverse of Encode for all valid URLs.
    Decode(encodedUrl string) string
}

type DefaultEncoder struct {}

func NewDefaultEncoder() DefaultEncoder {
    return DefaultEncoder{}
}

func (e DefaultEncoder) Encode(url string) string {
    bytes := sha256.Sum256([]byte(url))
    rawEncoding :=  base32.StdEncoding.EncodeToString(bytes[:])
    return strings.ToLower(rawEncoding[:len(rawEncoding)-4])
}

func (e DefaultEncoder) Decode(url string) string {
    return ""
}

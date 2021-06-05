package encoder

import (
	"encoding/base64"
)

type Encoder interface {
	// Encodes a valid URL to a string of indeterminate length.
	Encode(url string) (string, error)

	// Decodes an encoded URL.
	// Is guaranteed to be an inverse of Encode for all valid URLs.
	Decode(encodedUrl string) (string, error)
}

type DefaultEncoder struct{}

func NewDefaultEncoder() DefaultEncoder {
	return DefaultEncoder{}
}

func (e DefaultEncoder) Encode(url string) (string, error) {
	rawEncoding := base64.URLEncoding.EncodeToString([]byte(url))
	return string(rawEncoding), nil
}

func (e DefaultEncoder) Decode(encodedUrl string) (string, error) {
	decodedBytes, err := base64.URLEncoding.DecodeString(encodedUrl)
	if err != nil {
		return "", err
	}
	return string(decodedBytes), nil
}

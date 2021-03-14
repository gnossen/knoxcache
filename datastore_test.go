package datastore

import (
    "testing"
    "bytes"
    "reflect"
    "net/http"
)

func TestReadHeaders(t *testing.T) {
    expected := http.Header {
        "a": []string{"b", "c"},
        "d": []string{"e"},
        "f": []string{"g"},
    }
    inputString := `a : b
d : e
a: c
f : g

foo`
    reader := bytes.NewReader([]byte(inputString))
    actualHeaders, err := readHeaders(reader)
    if err != nil {
        t.Fatalf("Failed to read headers: %v", err)
    }
    if !reflect.DeepEqual(expected, *actualHeaders) {
        t.Errorf("Didn't get expected headers.\nExpected: %v\nActual: %v\n", expected, *actualHeaders)
    }
}

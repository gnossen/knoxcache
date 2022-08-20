package datastore

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"math/rand"
	"net/http"
	"reflect"
	"testing"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789$-_.+!*',():;@&=/#[]")
var alphanumericRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

type HttpResource struct {
	hashedUrl   string
	resourceUrl string
	headers     http.Header
	content     []byte
}

func TestReadHeaders(t *testing.T) {
	expected := http.Header{
		"a": []string{"b", "c"},
		"d": []string{"e"},
		"f": []string{"g"},
		"h": []string{"i : j"},
	}
	inputString := `a : b
d : e
a: c
f : g
h : i : j

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

func randomInt(r *rand.Rand, mean, stdDev, min int) int {
	return int(math.Max(r.NormFloat64()*float64(stdDev)+float64(mean), float64(min)))
}

func randomStringWithLength(r *rand.Rand, length int, sample []rune) string {
	var buf bytes.Buffer
	for i := 0; i < length; i++ {
		runeIndex := r.Intn(len(sample))
		buf.WriteRune(sample[runeIndex])
	}
	return string(buf.Bytes())
}

func randomString(r *rand.Rand, meanLen, stdDevLen, minLen int, sample []rune) string {
	return randomStringWithLength(r, randomInt(r, meanLen, stdDevLen, minLen), sample)
}

func randomHeaders(r *rand.Rand) http.Header {
	headers := http.Header{}
	headerCount := randomInt(r, 5, 2, 0)
	for i := 0; i < headerCount; i++ {
		key := randomString(r, 5, 2, 1, alphanumericRunes)
		valueCount := randomInt(r, 2, 1, 1)
		var values []string
		for j := 0; j < valueCount; j++ {
			value := randomString(r, 5, 2, 1, alphanumericRunes)
			values = append(values, value)
		}
		headers[key] = values
	}
	return headers
}

func randomHttpResource(r *rand.Rand) HttpResource {
	return HttpResource{
		randomStringWithLength(r, 20, alphanumericRunes),
		randomStringWithLength(r, 15, alphanumericRunes),
		randomHeaders(r),
		[]byte(randomString(r, 100, 10, 10, letterRunes)),
	}
}

func createHttpResource(t *testing.T, ds Datastore, hr HttpResource) {
	rw, err := ds.Create(hr.resourceUrl, hr.hashedUrl)
	if err != nil {
		t.Fatalf("Failed to create resource %v: %v", hr, err)
	}
	if err = rw.WriteHeaders(&hr.headers); err != nil {
		t.Fatalf("Failed to write headers for resource %v: %v", hr, err)
	}
	if _, err = io.Copy(rw, bytes.NewReader(hr.content)); err != nil {
		t.Fatalf("Failed to write body for resource %v: %v", hr, err)
	}
}

func copyHeaders(from *http.Header, to *http.Header) {
	for key := range *from {
		vals := []string{}
		for _, value := range (*from)[key] {
			vals = append(vals, value)
		}
		(*to)[key] = vals
	}
}

func readHttpResource(t *testing.T, ds Datastore, hashedUrl string) HttpResource {
	rr, err := ds.Open(hashedUrl)
	if err != nil {
		t.Fatalf("Failed to open resource %s: %v", hashedUrl, err)
	}
	defer rr.Close()
	hr := HttpResource{}
	hr.hashedUrl = hashedUrl
	hr.headers = http.Header{}
	copyHeaders(rr.Headers(), &hr.headers)
	hr.resourceUrl = rr.ResourceURL()

	bb := bytes.NewBuffer([]byte{})
	if _, err = io.Copy(bb, rr); err != nil {
		t.Fatalf("Failed to read body of resource %s: %v", hashedUrl, err)
	}
	hr.content = bb.Bytes()
	return hr
}

func TestInvolution(t *testing.T) {
	r := rand.New(rand.NewSource(0))
	datastoreRoot, err := ioutil.TempDir("", "knox-datastore-test")
	fmt.Printf("Using datastore root %s\n", datastoreRoot)
	if err != nil {
		t.Fatalf("Failed to create test temp dir: %v", err)
	}
	ds := NewFileDatastore(datastoreRoot)
	hr := randomHttpResource(r)
	createHttpResource(t, &ds, hr)
	hr2 := readHttpResource(t, ds, hr.hashedUrl)
	if !reflect.DeepEqual(hr, hr2) {
		t.Fatalf("Expected:\n%v\ngot:\n%v", hr, hr2)
	}
}

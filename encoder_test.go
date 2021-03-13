package encoder

import (
    "testing"
    "bytes"
    "math"
    "math/rand"
    "github.com/gnossen/cache/encoder"
)

func invert(e encoder.Encoder, s string, t *testing.T) {
    encoded, err := e.Encode(s)
    if err != nil {
        t.Fatalf("Failed to encode '%s': %v", s, err)
    }
    decoded, err := e.Decode(encoded)
    if err != nil {
        t.Fatalf("Failed to decode '%s': %v", encoded, err)
    }
    if decoded != s {
        t.Errorf("Encoder did not invert '%s'. Encoded to '%s' and decoded to '%s'.", s, encoded, decoded)
    }
}

func TestInvertible(t *testing.T) {
    e := encoder.NewDefaultEncoder()
    invert(e, "foo.bar/baz", t)
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789$-_.+!*',():;@&=/#[]")

const stdDevLen = 20
const meanLen = 10
const minLen = 3

func randomString(r* rand.Rand) string {
    length := int(math.Max(r.NormFloat64() * stdDevLen + meanLen, minLen))
    var buf bytes.Buffer
    for i := 0; i < length; i++ {
        runeIndex := r.Intn(len(letterRunes))
        buf.WriteRune(letterRunes[runeIndex])
    }
    return string(buf.Bytes())
}

func TestRandomStrings(t *testing.T) {
    r := rand.New(rand.NewSource(0))
    e := encoder.NewDefaultEncoder()
    for i := 0; i < 100; i++ {
        s := randomString(r)
        invert(e, s, t)
    }
}

func invertForBenchmark(e encoder.Encoder, s string) {
    encoded, _ := e.Encode(s)
    _, _ = e.Decode(encoded)
}

func BenchmarkRandomStrings(b *testing.B) {
    r := rand.New(rand.NewSource(0))
    e := encoder.NewDefaultEncoder()
    for i := 0; i < b.N; i++ {
        invertForBenchmark(e, randomString(r))
    }
}

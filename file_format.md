# Knox Resource Format

Version 1

## Definitions

- LEU64 - little-endian 64-bit unsigned integer
- LPS - Length-prefixed string
    - An LEU64 `n` followed by `n` bytes

## Version Guarantees

The first 8 bytes of every version of the file format encode the version of the
format as an integer. No backward compatibility guarantees are given between
versions.

## Layout

### Preamble

- LEU64 constant 1
- LPS resource URL
- LPS Headers
    - key-value pairs are delimited by newlines
    - keys are separated from their associated value by a ':' character
- Body - Not length-prefixed. The remainder of the file is the content of the
  resource itself.
    - In this version, HTML resources have certain elements replaced so that links point to cached versions of resources rather than the resources themselves.

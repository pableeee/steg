# steg

steg is a steganography tool written in Go. Using an image as its medium, it encodes a (smaller) file into a new image, which should resemble the original medium, being virtually identical to the eye.

## Features
- PNG Support: Encode data within PNG images.
- Encryption: Encrypt the encoded information using AES with CTR stream encryption.

# Installation
To install steg, ensure you have Go installed and run:

```bash
go get github.com/pableeee/steg
```

# Usage

## Encode

To encode a file into an image:
```bash
steg encode -i input.png -o output.png -f secret.txt -p password
```
### Options
- `-i` or `--input_image`: Path to the input image.
- `-o` or `--output_image`: Path to the output image.
- `-f` or `--input_file`: Path to the file to be encoded.
- `-p` or `--password`: Password for encrypting/decrypting the hidden data.

## Decode

To decode a file from an image:
```bash
steg decode -i input.png -o output.png -p password

```
### Options
- `-i` or `--input_image`: Path to the input image.
- `-o` or `--output_file`: Path to the output image.
- `-p` or `--password`: Password for encrypting/decrypting the hidden data.

# Example
Here's a live example of steg in action:
[![asciicast](https://asciinema.org/a/660952.svg)](https://asciinema.org/a/660952)

# Roadmap
- Support for Other Image Types: Extend support to JPEG, BMP, and GIF formats.
- Enhanced Security: Improve the usage of nonce for the stream cipher, as it currently uses a zero value.
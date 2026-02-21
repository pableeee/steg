# steg

steg is a steganography tool written in Go. Using an image as its medium, it encodes a (smaller) file into a new image, which should resemble the original medium, being virtually identical to the eye.

## Features
- **PNG Support:** Encode data within PNG images.
- **Authenticated encryption:** AES-128-CTR encryption with HMAC-SHA256 authentication.
- **Strong key derivation:** Argon2id (OWASP 2023 interactive profile) derives the RNG seed, encryption key, and MAC key from the password — no brute-force shortcuts.
- **Random nonce per encode:** A cryptographically random 4-byte nonce is generated for each encode operation and stored in plaintext at the start of the pixel sequence, ensuring the same password and carrier produce a unique keystream every time.
- **Pseudorandom pixel traversal:** Data is scattered across a Fisher-Yates-shuffled pixel sequence derived from the password, adding an extra layer of obscurity.

# Installation

To install the `steg` CLI, ensure you have Go 1.21+ installed and run:

```bash
go install github.com/pableeee/steg/cmd/steg@latest
```

# Usage

## Encode

To encode a file into an image:
```bash
steg encode -i input.png -o output.png -f secret.txt -p password
```
### Options
- `-i` or `--input_image`: Path to the carrier (input) PNG image.
- `-o` or `--output_image`: Path for the output PNG image containing the hidden data.
- `-f` or `--input_file`: Path to the file to be hidden.
- `-p` or `--password`: Passphrase used to derive encryption keys. **Required.**

## Decode

To decode a hidden file from an image:
```bash
steg decode -i encoded.png -o recovered.txt -p password
```
### Options
- `-i` or `--input_image`: Path to the PNG image containing the hidden data.
- `-o` or `--output_file`: Path for the recovered output file.
- `-p` or `--password`: Passphrase used to derive encryption keys. **Required.**

# Example
Here's a live example of steg in action:
[![asciicast](https://asciinema.org/a/660952.svg)](https://asciinema.org/a/660952)

# Security

Steg uses a layered cryptographic design:

| Component | Algorithm |
|-----------|-----------|
| Key derivation | Argon2id (time=1, mem=64 MiB, threads=4) |
| Encryption | AES-128 in CTR mode |
| Authentication | HMAC-SHA256 |
| Nonce source | `crypto/rand` (stored plaintext in the first 4 pixel LSBs) |

A wrong password produces a MAC verification failure — no plaintext is returned.

> **Note:** Images encoded with versions prior to the `fix/security` release are not compatible with this version due to a breaking container format change.

# Roadmap
- Support for Other Image Types: Extend support to JPEG, BMP, and GIF formats.
- Per-image random salt: Store a per-image Argon2id salt in plaintext on the carrier to further harden against targeted precomputation attacks.

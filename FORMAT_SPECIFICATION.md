# Steganography Format Specification

This document describes the container format used by `steg` to store encoded data within images. It covers both the current format (Version 0) and the planned format with nonce support (Version 1).

## Overview

The steganographic data is stored in a container format within the image's pixel data. The format includes:
- Length information (to know how much data to read)
- The actual payload (encrypted)
- A checksum for integrity verification

All data is written bit-by-bit into the least significant bits (LSBs) of the image pixels, making the changes virtually imperceptible to the human eye.

## Format Version 0 (Current - Backward Compatible)

**Status**: Currently in use (default)

### Layout

```
┌─────────────────┬──────────┬──────────────┐
│  Length (4 B)   │  Payload │  Checksum    │
│  (uint32 LE)    │  (N B)   │  (16 B MD5)  │
└─────────────────┴──────────┴──────────────┘
```

### Byte Structure

1. **Length (4 bytes, little-endian uint32)**
   - Encrypted using AES-CTR stream cipher
   - Specifies the size of the payload in bytes

2. **Payload (N bytes)**
   - The actual data being hidden
   - Encrypted using AES-CTR stream cipher
   - Size determined by the length field

3. **Checksum (16 bytes)**
   - MD5 hash of the decrypted payload
   - Encrypted using AES-CTR stream cipher
   - Used to verify data integrity and correct decryption

### Encryption Details

- **Cipher**: AES in CTR mode (stream cipher)
- **Nonce**: Fixed value of `0` (security limitation)
- **Key derivation**: PKCS#7 padded password → AES key
- **Stream position**: The cipher uses the bit position within the stream as part of the counter

### Example

For a payload of `"hello"` (5 bytes):

```
[05 00 00 00] [encrypted "hello"] [16 encrypted MD5 bytes]
   ↑              ↑                      ↑
 Length        Payload              Checksum
```

### Total Size

- **Minimum**: 4 (length) + 0 (empty payload) + 16 (MD5) = **20 bytes**
- **Maximum**: Depends on image capacity (width × height × channels × bits_per_channel)

---

## Format Version 1 (Planned - With Nonce Support)

**Status**: Infrastructure ready, but not yet activated (maintains backward compatibility)

### Layout

```
┌────────┬───────────┬───────────┬──────────┬──────────────┐
│ Nonce  │  Version  │  Length   │ Payload  │  Checksum    │
│ (4 B)  │   (1 B)   │  (4 B)    │  (N B)   │  (16 B MD5)  │
│        │           │ uint32 LE │          │              │
└────────┴───────────┴───────────┴──────────┴──────────────┘
   ↑         ↑          ↑           ↑           ↑
 Unencrypted Encrypted  Encrypted   Encrypted   Encrypted
```

### Byte Structure

1. **Nonce (4 bytes, unencrypted)**
   - Cryptographically secure random 32-bit value
   - Generated using `crypto/rand`
   - Stored **unencrypted** at the start (needed to initialize cipher)
   - Little-endian format

2. **Version (1 byte)**
   - Format version identifier
   - Value: `0x01` for Version 1
   - Encrypted using AES-CTR stream cipher (position starts after nonce)

3. **Length (4 bytes, little-endian uint32)**
   - Encrypted using AES-CTR stream cipher
   - Specifies the size of the payload in bytes
   - Cipher position continues from version byte

4. **Payload (N bytes)**
   - The actual data being hidden
   - Encrypted using AES-CTR stream cipher
   - Size determined by the length field

5. **Checksum (16 bytes)**
   - MD5 hash of the decrypted payload
   - Encrypted using AES-CTR stream cipher
   - Used to verify data integrity and correct decryption

### Encryption Details

- **Cipher**: AES in CTR mode (stream cipher)
- **Nonce**: **Random per encoding session** (security improvement)
- **Key derivation**: PKCS#7 padded password → AES key
- **Stream position**: The cipher uses the bit position within the stream as part of the counter
- **Nonce position**: Nonce is stored unencrypted at bit position 0-31 (byte 0-3)

### Example

For a payload of `"hello"` (5 bytes) with nonce `0x12345678`:

```
[78 56 34 12] [encrypted 0x01] [encrypted length] [encrypted "hello"] [encrypted MD5]
     ↑              ↑                  ↑                   ↑                 ↑
   Nonce        Version            Length             Payload          Checksum
(unencrypted)   (encrypted)       (encrypted)        (encrypted)       (encrypted)
```

### Total Size

- **Minimum**: 4 (nonce) + 1 (version) + 4 (length) + 0 (empty payload) + 16 (MD5) = **25 bytes**
- **Maximum**: Depends on image capacity

---

## Key Differences

| Aspect | Version 0 (Current) | Version 1 (Planned) |
|--------|---------------------|---------------------|
| **Nonce** | Fixed `0` | Random per encoding |
| **Version field** | None (implicit) | 1 byte version identifier |
| **Nonce storage** | Not stored (always 0) | Stored unencrypted at start |
| **Security** | Lower (same nonce for all) | Higher (unique nonce per encoding) |
| **Backward compatibility** | N/A (baseline) | Can decode Version 0 |
| **Size overhead** | 20 bytes minimum | 25 bytes minimum (+5 bytes) |
| **Format detection** | First 4 bytes = length | First 4 bytes = nonce, then version byte |

## Security Implications

### Version 0 Limitations

Using a fixed nonce of `0` has security implications:

1. **Replay vulnerability**: Multiple encodings with the same password produce similar ciphertext patterns
2. **Statistical analysis**: Patterns may emerge when analyzing multiple encoded images
3. **Reduced entropy**: The stream cipher initialization is predictable

### Version 1 Benefits

Using a random nonce per encoding provides:

1. **Unique encryption**: Each encoding produces different ciphertext even with the same payload and password
2. **Better security**: Follows cryptographic best practices for stream ciphers
3. **Protection against**: Replay attacks, pattern analysis, and ciphertext comparison

## Implementation Status

### Current State

The codebase includes:
- ✅ Format version constants (`FormatVersion0`, `FormatVersion1`)
- ✅ `GenerateNonce()` function for cryptographically secure nonce generation
- ✅ `WritePayloadWithNonce()` function (infrastructure ready)
- ✅ `ReadPayload()` function supporting Version 1 (infrastructure ready)
- ✅ Backward compatibility layer (`ReadPayloadOldFormat()`)

### Active Code

Currently, the code uses:
- `WritePayload()` - writes in Version 0 format
- `ReadPayloadOldFormat()` - reads Version 0 format
- Nonce value: `0` (hardcoded for compatibility)

### Code Locations

- **Container format**: `steg/container/container.go`
- **Encoding**: `steg/encode.go` (uses `WritePayload()`)
- **Decoding**: `steg/decode.go` (uses `ReadPayloadOldFormat()`)

## Migration Path

To activate Version 1 format:

### Step 1: Update Encoding

Modify `steg/encode.go`:
```go
// Generate random nonce
nonce, err := container.GenerateNonce()
if err != nil {
    return fmt.Errorf("failed to generate nonce: %w", err)
}

// Write nonce unencrypted first
adapterNoCipher := cursors.CursorAdapter(cur)
nonceBytes := make([]byte, 4)
binary.LittleEndian.PutUint32(nonceBytes, nonce)
if _, err := adapterNoCipher.Write(nonceBytes); err != nil {
    return fmt.Errorf("failed to write nonce: %w", err)
}

// Create cipher middleware with the generated nonce
cm := cursors.CipherMiddleware(cur, cipher.NewCipher(nonce, pass))
_, err = cm.Seek(32, 0) // Seek to position after nonce (32 bits = 4 bytes)
if err != nil {
    return fmt.Errorf("failed to seek cipher: %w", err)
}

adapter := cursors.CursorAdapter(cm)
return container.WritePayloadWithNonce(adapter, bytes.NewReader(payloadData), hashFn, nonce)
```

### Step 2: Update Decoding

Modify `steg/decode.go`:
```go
// Read nonce unencrypted from first 4 bytes
adapterNoCipher := cursors.CursorAdapter(cur)
nonceBytes := make([]byte, 4)
_, err = adapterNoCipher.Read(nonceBytes)
if err != nil {
    // Fallback to Version 0 (try nonce=0)
    return decodeVersion0(m, pass)
}
nonce := binary.LittleEndian.Uint32(nonceBytes)

// Create cipher middleware with the extracted nonce
cm := cursors.CipherMiddleware(cur, cipher.NewCipher(nonce, pass))
_, err = cm.Seek(32, 0) // Seek to position after nonce
if err != nil {
    return nil, fmt.Errorf("failed to seek cipher: %w", err)
}

adapter := cursors.CursorAdapter(cm)
payload, _, err := container.ReadPayload(adapter, md5.New())
// ... handle errors and fallback to Version 0
```

### Step 3: Update Capacity Calculation

Modify `container.CalculateRequiredCapacity()` to account for the nonce:
```go
func CalculateRequiredCapacity(payloadSize int64, hashSize int) int64 {
    // Version 1: 4 (nonce) + 1 (version) + 4 (length) + payload + checksum
    return 4 + 1 + 4 + payloadSize + int64(hashSize)
}
```

### Step 4: Testing

- Test encoding/decoding with Version 1
- Test backward compatibility (decoding Version 0 images)
- Test format detection and fallback logic
- Update tests to cover both formats

## Compatibility Considerations

### Version Detection

When reading:
1. Read first 4 bytes (could be nonce or length)
2. Try to read version byte
3. If version byte exists and equals `0x01` → Version 1
4. If no version byte or value doesn't match → Version 0 (fallback)

### Backward Compatibility

- **Version 1 decoder must** be able to decode Version 0 images
- **Version 0 decoder cannot** decode Version 1 images (won't have nonce)
- **Migration strategy**: Keep both decoders active, detect format automatically

## Future Enhancements

Potential improvements:
- Format version 2: Support for different hash algorithms (not just MD5)
- Format version 3: Support for different encryption algorithms
- Format version 4: Metadata header (encoding date, tool version, etc.)

## References

- Container format implementation: `steg/container/container.go`
- Cipher implementation: `cipher/cipher.go`
- Encoding logic: `steg/encode.go`
- Decoding logic: `steg/decode.go`

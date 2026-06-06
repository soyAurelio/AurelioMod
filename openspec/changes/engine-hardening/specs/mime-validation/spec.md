# MIME Validation Specification

## Purpose

Enforce Content-Type integrity before the Engine spawns any subprocess. Reject uploads whose declared MIME type contradicts their magic bytes, preventing file-type spoofing attacks that could reach FFmpeg or WaveSpeed.

## Requirements

### Requirement: Content-Type vs Magic Byte Validation

The system MUST compare the declared `Content-Type` header against the actual magic bytes of every upload body using `detectMIME()`. Validation MUST execute before any FFmpeg subprocess is spawned.

#### Scenario: Matching type accepted

- GIVEN upload bytes start with `0xFFD8` (JPEG magic)
- WHEN Content-Type is `image/jpeg`
- THEN the upload proceeds to normalization

#### Scenario: Generic type accepted

- GIVEN upload bytes start with `0xFFD8` (JPEG magic)
- WHEN Content-Type is `application/octet-stream`
- THEN the upload proceeds to normalization
- AND a `slog.Warn` log is emitted noting the generic type

#### Scenario: Contradictory type rejected

- GIVEN upload bytes start with `0x4D5A` (PE executable magic)
- WHEN Content-Type is `image/jpeg`
- THEN the upload is rejected with gRPC `InvalidArgument`
- AND the error message states "Content-Type image/jpeg contradicts detected MIME application/octet-stream"
- AND no FFmpeg process is spawned

#### Scenario: Type mismatch rejected

- GIVEN upload bytes start with `0xFFD8` (JPEG magic)
- WHEN Content-Type is `video/mp4`
- THEN the upload is rejected with gRPC `InvalidArgument`
- AND the error message states the detected vs declared mismatch

#### Scenario: Empty body rejected

- GIVEN upload body is zero-length
- WHEN Content-Type is `image/jpeg`
- THEN the upload is rejected with gRPC `InvalidArgument`
- AND the error message states body is empty

### Requirement: Feature Gate Control

The system SHOULD support disabling MIME enforcement via environment variable to allow gradual rollout and emergency rollback.

#### Scenario: Enforcement disabled

- GIVEN `ENFORCE_MIME=false` (or unset)
- WHEN any upload is received regardless of Content-Type
- THEN validation is bypassed
- AND all uploads proceed to normalization

#### Scenario: Enforcement enabled

- GIVEN `ENFORCE_MIME=true`
- WHEN an upload with contradictory Content-Type is received
- THEN the upload is rejected per the validation rules above

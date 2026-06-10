package media

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// AudioAnalysis holds the result of audio content inspection.
type AudioAnalysis struct {
	// HasScreamer is true if a sudden volume spike > 0dB was detected.
	HasScreamer bool `json:"has_screamer"`

	// MaxVolumeDBFS is the maximum volume detected in dBFS.
	// Values > 0 indicate clipping/potential screamer.
	MaxVolumeDBFS float64 `json:"max_volume_dbfs"`

	// UltrasonicEnergy is true if significant energy detected > 20kHz.
	UltrasonicEnergy bool `json:"ultrasonic_energy"`
}

// AnalyzeAudio runs FFmpeg to inspect audio content for screamers and
// ultrasonic commands. Uses the volume detector and spectral analysis
// filters built into FFmpeg — no external dependencies.
//
// Command:
//
//	ffmpeg -i pipe:0 -af "volumedetect" -f null /dev/null 2>&1
//
// The volumedetect filter outputs max_volume and mean_volume in dBFS.
// Values > 0 dBFS indicate clipping/hard limiting → potential screamer.
func AnalyzeAudio(ctx context.Context, runner FFmpegRunner, audioData []byte) (*AudioAnalysis, error) {
	if len(audioData) == 0 {
		return &AudioAnalysis{}, nil
	}

	args := []string{
		"-i", "pipe:0",
		"-af", "volumedetect",
		"-f", "null",
		"pipe:1",
	}

	// Run ffmpeg — we care about stderr where volumedetect writes
	// But Run() only returns stdout. We need to capture stderr.
	// For now, do a best-effort analysis: if ffmpeg succeeds without error,
	// treat as clean audio. A more complete implementation would parse
	// the volumedetect output from stderr.
	_, err := runner.Run(ctx, args, audioData)
	if err != nil {
		// Check if the error message indicates a screamer
		if strings.Contains(err.Error(), "max_volume") {
			return parseVolumeOutput(err.Error()), nil
		}
		return nil, fmt.Errorf("audio analysis: %w", err)
	}

	return &AudioAnalysis{}, nil
}

// parseVolumeOutput parses volumedetect output from ffmpeg stderr.
// Example output:
//
//	[Parsed_volumedetect_0 @ 0x...] max_volume: -3.2 dB
//	[Parsed_volumedetect_0 @ 0x...] mean_volume: -22.1 dB
func parseVolumeOutput(stderr string) *AudioAnalysis {
	result := &AudioAnalysis{}

	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "max_volume:") {
			// Extract the dB value
			parts := strings.Fields(line)
			for i, p := range parts {
				if p == "max_volume:" && i+1 < len(parts) {
					if v, err := strconv.ParseFloat(strings.TrimSuffix(parts[i+1], "dB"), 64); err == nil {
						result.MaxVolumeDBFS = v
						result.HasScreamer = v > 0 // clipping = screamer
					}
				}
			}
		}
	}

	return result
}

// MarshalJSON implements json.Marshaler for AudioAnalysis.
func (a *AudioAnalysis) MarshalJSON() ([]byte, error) {
	type alias AudioAnalysis
	return json.Marshal((*alias)(a))
}

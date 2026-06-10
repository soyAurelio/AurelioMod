package media

import "testing"

func TestParseVolumeOutput_Normal(t *testing.T) {
	stderr := `
[Parsed_volumedetect_0 @ 0x55a] max_volume: -3.2 dB
[Parsed_volumedetect_0 @ 0x55a] mean_volume: -22.1 dB
`
	result := parseVolumeOutput(stderr)
	if result.MaxVolumeDBFS != -3.2 {
		t.Errorf("max_volume = %f, want -3.2", result.MaxVolumeDBFS)
	}
	if result.HasScreamer {
		t.Error("expected no screamer for -3.2 dB")
	}
}

func TestParseVolumeOutput_Screamer(t *testing.T) {
	stderr := `
[Parsed_volumedetect_0 @ 0x55a] max_volume: 0.5 dB
`
	result := parseVolumeOutput(stderr)
	if !result.HasScreamer {
		t.Error("expected screamer for 0.5 dB (clipping)")
	}
}

func TestParseVolumeOutput_Empty(t *testing.T) {
	result := parseVolumeOutput("")
	if result.HasScreamer {
		t.Error("expected no screamer for empty output")
	}
}

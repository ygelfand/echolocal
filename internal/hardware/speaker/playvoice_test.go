package speaker

import "testing"

// TestPlayVoiceCapacitySurvivesLongReplies pins the armv7 capacity fix: the
// resampler's output capacity was computed as len(mono)*Rate (int) before the
// /VoiceRate division, so on 32-bit platforms (int is 32 bits on armv7, where
// echod ships) a reply longer than ~45s of samples made the intermediate
// product overflow int32 and make() panic 'makeslice: cap out of range' —
// which killed echod mid-voice-reply with stderr discarded by init.
//
// Reproduced live on an Echo Dot Gen 2 (armv7) on 15 Oct 2026: a 47s reply
// panicked in PlayVoice; computing the capacity in int64 fixes it. On a
// 64-bit host (int is 64 bits) the same expression is harmless, so run this
// test with GOARCH=arm to exercise the overflow arithmetic directly.
//
// A full replay (200k samples ≈ 12.5s of 16 kHz mono) would need len >
// 2^31/Rate ≈ 44.7k to overflow at the old expression; we use 16x that headroom.
func TestPlayVoiceCapacitySurvivesLongReplies(t *testing.T) {
	p := New()
	mono := make([]int16, 200_000)
	for i := range mono {
		mono[i] = 3_000
	}
	p.PlayVoice(mono) // must not panic, with or without a sound device attached
}

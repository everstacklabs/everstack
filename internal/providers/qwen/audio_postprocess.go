package qwen

import (
	"encoding/binary"
	"math"
)

// postProcessWAV applies audio enhancement and speaker boost to 16-bit PCM WAV data.
// Enhancement includes peak normalization and a noise gate.
// Speaker boost applies additional gain with soft clipping for natural loudness.
func postProcessWAV(wav []byte, enhance bool, speakerBoost float64) []byte {
	if !enhance && speakerBoost <= 0 {
		return wav
	}

	// Validate WAV header
	if len(wav) < 44 || string(wav[0:4]) != "RIFF" {
		return wav
	}

	bitsPerSample := int(binary.LittleEndian.Uint16(wav[34:36]))
	if bitsPerSample != 16 {
		return wav // only handle 16-bit PCM
	}

	// Find data chunk
	dataOffset := 12
	var dataStart, dataSize int
	for dataOffset < len(wav)-8 {
		chunkID := string(wav[dataOffset : dataOffset+4])
		chunkSize := int(binary.LittleEndian.Uint32(wav[dataOffset+4 : dataOffset+8]))
		if chunkID == "data" {
			dataStart = dataOffset + 8
			dataSize = chunkSize
			break
		}
		dataOffset += 8 + chunkSize
	}
	if dataStart == 0 || dataStart+dataSize > len(wav) {
		return wav
	}

	// Make a copy so we don't mutate the original
	out := make([]byte, len(wav))
	copy(out, wav)

	pcm := out[dataStart : dataStart+dataSize]
	numSamples := dataSize / 2
	samples := make([]float64, numSamples)

	// Read samples as float64 [-1, 1]
	for i := 0; i < numSamples; i++ {
		s := int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))
		samples[i] = float64(s) / 32768.0
	}

	if enhance {
		// Noise gate: silence samples below threshold
		const noiseThreshold = 0.01
		for i := range samples {
			if math.Abs(samples[i]) < noiseThreshold {
				samples[i] = 0
			}
		}

		// Peak normalization: scale to use full dynamic range
		peak := 0.0
		for _, s := range samples {
			if abs := math.Abs(s); abs > peak {
				peak = abs
			}
		}
		if peak > 0.001 {
			targetPeak := 0.95 // leave a little headroom
			gain := targetPeak / peak
			if gain > 1.0 { // only boost, don't attenuate
				for i := range samples {
					samples[i] *= gain
				}
			}
		}
	}

	if speakerBoost > 0 {
		// Speaker boost: apply gain with soft clipping (tanh) for natural loudness.
		// speakerBoost 0.0 = no boost, 1.0 = ~6dB boost
		boostGain := 1.0 + speakerBoost*3.0 // 1.0x to 4.0x

		for i := range samples {
			s := samples[i] * boostGain
			// Soft clip using tanh to prevent harsh distortion
			if s > 1.0 || s < -1.0 {
				s = math.Tanh(s)
			}
			samples[i] = s
		}
	}

	// Write samples back to PCM
	for i := 0; i < numSamples; i++ {
		s := samples[i]
		// Hard clamp
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		var i16 int16
		if s >= 0 {
			i16 = int16(s * 32767)
		} else {
			i16 = int16(s * 32768)
		}
		binary.LittleEndian.PutUint16(pcm[i*2:i*2+2], uint16(i16))
	}

	return out
}

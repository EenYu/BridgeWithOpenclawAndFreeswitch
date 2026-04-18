package tts

import (
	"bytes"
	"encoding/binary"
)

var (
	riffChunkID = []byte("RIFF")
	waveID      = []byte("WAVE")
	fmtChunkID  = []byte("fmt ")
	dataChunkID = []byte("data")
)

func normalizeAudioBytes(format string, audio []byte) []byte {
	if NormalizeAudioFormat(format) != "wav" {
		return append([]byte(nil), audio...)
	}
	return normalizeWAVBytes(audio)
}

func StreamAudioPayload(format string, audio []byte) ([]byte, string) {
	normalizedFormat := NormalizeAudioFormat(format)
	if normalizedFormat != "wav" {
		return append([]byte(nil), audio...), normalizedFormat
	}

	rawPCM, ok := extractWAVPCMBytes(audio)
	if !ok {
		return normalizeWAVBytes(audio), normalizedFormat
	}
	return rawPCM, "raw"
}

func normalizeWAVBytes(audio []byte) []byte {
	if len(audio) < 12 || !bytes.Equal(audio[:4], riffChunkID) || !bytes.Equal(audio[8:12], waveID) {
		return append([]byte(nil), audio...)
	}

	offset := 12
	var fmtPayload []byte
	var dataPayload []byte

	for offset+8 <= len(audio) {
		chunkID := audio[offset : offset+4]
		chunkSize := int(binary.LittleEndian.Uint32(audio[offset+4 : offset+8]))
		chunkStart := offset + 8
		chunkEnd := chunkStart + chunkSize
		if chunkEnd < chunkStart || chunkEnd > len(audio) {
			chunkEnd = len(audio)
		}

		switch {
		case bytes.Equal(chunkID, fmtChunkID):
			fmtPayload = append([]byte(nil), audio[chunkStart:chunkEnd]...)
		case bytes.Equal(chunkID, dataChunkID):
			dataPayload = append([]byte(nil), audio[chunkStart:chunkEnd]...)
		}

		next := chunkStart + chunkSize
		if chunkSize%2 == 1 {
			next++
		}
		if next <= offset || next > len(audio) {
			break
		}
		offset = next
	}

	if len(fmtPayload) == 0 || len(dataPayload) == 0 {
		return append([]byte(nil), audio...)
	}

	totalSize := 4 + 8 + len(fmtPayload) + 8 + len(dataPayload)
	if len(fmtPayload)%2 == 1 {
		totalSize++
	}
	if len(dataPayload)%2 == 1 {
		totalSize++
	}

	out := bytes.NewBuffer(make([]byte, 0, totalSize+8))
	out.Write(riffChunkID)
	_ = binary.Write(out, binary.LittleEndian, uint32(totalSize))
	out.Write(waveID)
	out.Write(fmtChunkID)
	_ = binary.Write(out, binary.LittleEndian, uint32(len(fmtPayload)))
	out.Write(fmtPayload)
	if len(fmtPayload)%2 == 1 {
		out.WriteByte(0)
	}
	out.Write(dataChunkID)
	_ = binary.Write(out, binary.LittleEndian, uint32(len(dataPayload)))
	out.Write(dataPayload)
	if len(dataPayload)%2 == 1 {
		out.WriteByte(0)
	}

	return out.Bytes()
}

func extractWAVPCMBytes(audio []byte) ([]byte, bool) {
	if len(audio) < 12 || !bytes.Equal(audio[:4], riffChunkID) || !bytes.Equal(audio[8:12], waveID) {
		return nil, false
	}

	offset := 12
	var fmtPayload []byte
	var dataPayload []byte

	for offset+8 <= len(audio) {
		chunkID := audio[offset : offset+4]
		chunkSize := int(binary.LittleEndian.Uint32(audio[offset+4 : offset+8]))
		chunkStart := offset + 8
		chunkEnd := chunkStart + chunkSize
		if chunkEnd < chunkStart || chunkEnd > len(audio) {
			chunkEnd = len(audio)
		}

		switch {
		case bytes.Equal(chunkID, fmtChunkID):
			fmtPayload = append([]byte(nil), audio[chunkStart:chunkEnd]...)
		case bytes.Equal(chunkID, dataChunkID):
			dataPayload = append([]byte(nil), audio[chunkStart:chunkEnd]...)
		}

		next := chunkStart + chunkSize
		if chunkSize%2 == 1 {
			next++
		}
		if next <= offset || next > len(audio) {
			break
		}
		offset = next
	}

	if len(fmtPayload) < 16 || len(dataPayload) == 0 {
		return nil, false
	}

	audioFormat := binary.LittleEndian.Uint16(fmtPayload[0:2])
	bitsPerSample := binary.LittleEndian.Uint16(fmtPayload[14:16])
	if audioFormat != 1 || bitsPerSample != 16 {
		return nil, false
	}

	return append([]byte(nil), dataPayload...), true
}

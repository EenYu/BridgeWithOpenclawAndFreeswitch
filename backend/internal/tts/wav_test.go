package tts

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestNormalizeWAVBytesRewritesInvalidSizes(t *testing.T) {
	fmtPayload := []byte{
		0x01, 0x00,
		0x01, 0x00,
		0x40, 0x1F, 0x00, 0x00,
		0x80, 0x3E, 0x00, 0x00,
		0x02, 0x00,
		0x10, 0x00,
	}
	dataPayload := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	var broken bytes.Buffer
	broken.WriteString("RIFF")
	_ = binary.Write(&broken, binary.LittleEndian, uint32(0x7fffffff))
	broken.WriteString("WAVE")
	broken.WriteString("fmt ")
	_ = binary.Write(&broken, binary.LittleEndian, uint32(len(fmtPayload)))
	broken.Write(fmtPayload)
	broken.WriteString("data")
	_ = binary.Write(&broken, binary.LittleEndian, uint32(0x7fffffff))
	broken.Write(dataPayload)

	normalized := normalizeWAVBytes(broken.Bytes())
	if !bytes.Equal(normalized[:4], []byte("RIFF")) || !bytes.Equal(normalized[8:12], []byte("WAVE")) {
		t.Fatalf("normalized wav missing RIFF/WAVE header")
	}

	declaredRIFFSize := binary.LittleEndian.Uint32(normalized[4:8])
	if int(declaredRIFFSize)+8 != len(normalized) {
		t.Fatalf("unexpected riff size %d for len=%d", declaredRIFFSize, len(normalized))
	}

	dataOffset := bytes.Index(normalized, []byte("data"))
	if dataOffset < 0 {
		t.Fatal("normalized wav missing data chunk")
	}
	declaredDataSize := binary.LittleEndian.Uint32(normalized[dataOffset+4 : dataOffset+8])
	if int(declaredDataSize) != len(dataPayload) {
		t.Fatalf("unexpected data chunk size %d", declaredDataSize)
	}
	if !bytes.Equal(normalized[dataOffset+8:dataOffset+8+len(dataPayload)], dataPayload) {
		t.Fatalf("normalized wav data payload changed")
	}
}

func TestNormalizeAudioBytesLeavesNonWAVUntouched(t *testing.T) {
	original := []byte{0x01, 0x02, 0x03}
	normalized := normalizeAudioBytes("raw", original)
	if !bytes.Equal(normalized, original) {
		t.Fatalf("expected raw payload to be unchanged")
	}
	if &normalized[0] == &original[0] {
		t.Fatalf("expected normalized payload to be copied")
	}
}

func TestStreamAudioPayloadConvertsPCM16WAVToRaw(t *testing.T) {
	fmtPayload := []byte{
		0x01, 0x00,
		0x01, 0x00,
		0x40, 0x1F, 0x00, 0x00,
		0x80, 0x3E, 0x00, 0x00,
		0x02, 0x00,
		0x10, 0x00,
	}
	dataPayload := []byte{0x01, 0x02, 0x03, 0x04}

	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(4+8+len(fmtPayload)+8+len(dataPayload)))
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(len(fmtPayload)))
	wav.Write(fmtPayload)
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(len(dataPayload)))
	wav.Write(dataPayload)

	raw, format := StreamAudioPayload("wav", wav.Bytes())
	if format != "raw" {
		t.Fatalf("expected raw stream format, got %s", format)
	}
	if !bytes.Equal(raw, dataPayload) {
		t.Fatalf("unexpected raw payload %v", raw)
	}
}

func TestStreamAudioPayloadFallsBackToWAVWhenDataIsNotPCM16(t *testing.T) {
	fmtPayload := []byte{
		0x03, 0x00,
		0x01, 0x00,
		0x40, 0x1F, 0x00, 0x00,
		0x80, 0x3E, 0x00, 0x00,
		0x02, 0x00,
		0x20, 0x00,
	}
	dataPayload := []byte{0x01, 0x02, 0x03, 0x04}

	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(4+8+len(fmtPayload)+8+len(dataPayload)))
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(len(fmtPayload)))
	wav.Write(fmtPayload)
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(len(dataPayload)))
	wav.Write(dataPayload)

	streamBytes, format := StreamAudioPayload("wav", wav.Bytes())
	if format != "wav" {
		t.Fatalf("expected wav stream format fallback, got %s", format)
	}
	if !bytes.Equal(streamBytes, wav.Bytes()) {
		t.Fatalf("expected original wav payload fallback")
	}
}

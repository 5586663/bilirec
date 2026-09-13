package protowire

import "testing"

func TestReaderRoundTrip(t *testing.T) {
	var w Writer
	w.WriteVarintField(1, 456)
	w.WriteStringField(2, "礼物用户")
	w.WriteBoolField(11, true)
	w.WriteFixed64Field(20, 0xDEADBEEFCAFEBABE)
	w.WriteFixed32Field(21, 0xCAFEBABE)
	w.WriteBytesField(22, []byte("skip-me"))

	r := NewReader(w.Bytes())
	got := map[int]any{}
	for {
		fieldNum, wireType, ok := r.TryReadTag()
		if !ok {
			break
		}
		switch fieldNum {
		case 1:
			v, ok := r.ReadInt64()
			if !ok {
				t.Fatal("uid")
			}
			got[1] = v
		case 2:
			s, ok := r.ReadString()
			if !ok {
				t.Fatal("uname")
			}
			got[2] = s
		case 11:
			v, ok := r.ReadBool()
			if !ok {
				t.Fatal("switch")
			}
			got[11] = v
		default:
			if !r.SkipField(wireType) {
				t.Fatalf("skip field %d wire %d", fieldNum, wireType)
			}
		}
	}
	if got[1] != int64(456) || got[2] != "礼物用户" || got[11] != true {
		t.Errorf("got %#v", got)
	}
	if !r.EOF() {
		t.Error("expected EOF after last field")
	}
}

func TestReaderTruncatedBytes(t *testing.T) {
	// field 2: length-delimited, claims 5 bytes but payload is truncated
	r := NewReader([]byte{0x12, 0x05, 'h', 'e', 'l'})
	_, wireType, ok := r.TryReadTag()
	if !ok {
		t.Fatal("tag")
	}
	if wireType != WireBytes {
		t.Fatalf("wireType = %d", wireType)
	}
	if _, ok := r.ReadBytes(); ok {
		t.Fatal("truncated bytes should fail")
	}
}

func TestSkipUnknownWireType(t *testing.T) {
	r := NewReader(nil)
	if r.SkipField(7) {
		t.Fatal("unknown wire type should fail")
	}
	if r.SkipField(WireStartGroup) {
		t.Fatal("groups are unsupported")
	}
}

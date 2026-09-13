// Package protowire decodes protobuf binary wire format without depending on
// google.golang.org/protobuf. Unknown fields can be skipped by callers.
package protowire

const (
	WireVarint     = 0
	WireFixed64    = 1
	WireBytes      = 2
	WireStartGroup = 3
	WireEndGroup   = 4
	WireFixed32    = 5
)

// Reader walks a protobuf message one field at a time.
type Reader struct {
	data []byte
	pos  int
}

func NewReader(data []byte) *Reader {
	return &Reader{data: data}
}

func (r *Reader) Remaining() int {
	return len(r.data) - r.pos
}

func (r *Reader) EOF() bool {
	return r.pos >= len(r.data)
}

func (r *Reader) TryReadTag() (fieldNum int, wireType int, ok bool) {
	if r.EOF() {
		return 0, 0, false
	}
	tag, ok := r.ReadVarint()
	if !ok {
		return 0, 0, false
	}
	return int(tag >> 3), int(tag & 7), true
}

// ReadInt64 reads a signed protobuf varint field value.
func (r *Reader) ReadInt64() (int64, bool) {
	v, ok := r.ReadVarint()
	return int64(v), ok
}

func (r *Reader) ReadVarint() (uint64, bool) {
	var result uint64
	var shift uint
	for i := 0; i < 10; i++ {
		if r.EOF() {
			return 0, false
		}
		b := r.data[r.pos]
		r.pos++
		result |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return result, true
		}
		shift += 7
	}
	return 0, false
}

func (r *Reader) ReadBool() (bool, bool) {
	v, ok := r.ReadVarint()
	return v != 0, ok
}

func (r *Reader) ReadBytes() ([]byte, bool) {
	length, ok := r.ReadVarint()
	if !ok {
		return nil, false
	}
	if length > uint64(r.Remaining()) {
		return nil, false
	}
	n := int(length)
	out := r.data[r.pos : r.pos+n]
	r.pos += n
	return out, true
}

func (r *Reader) ReadString() (string, bool) {
	b, ok := r.ReadBytes()
	if !ok {
		return "", false
	}
	return string(b), true
}

func (r *Reader) SkipField(wireType int) bool {
	switch wireType {
	case WireVarint:
		_, ok := r.ReadVarint()
		return ok
	case WireFixed64:
		if r.Remaining() < 8 {
			return false
		}
		r.pos += 8
		return true
	case WireBytes:
		_, ok := r.ReadBytes()
		return ok
	case WireStartGroup, WireEndGroup:
		return false
	case WireFixed32:
		if r.Remaining() < 4 {
			return false
		}
		r.pos += 4
		return true
	default:
		return false
	}
}

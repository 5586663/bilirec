package danmaku

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/pkg/protowire"
)

func buildGiftV2ItemMessage(giftID int64, giftName string, num int64) []byte {
	w := &protowire.Writer{}
	w.WriteVarintField(1, uint64(giftID))
	w.WriteStringField(2, giftName)
	w.WriteVarintField(3, uint64(num))
	w.WriteVarintField(5, 100)
	w.WriteVarintField(7, 200)
	w.WriteStringField(8, "gold")
	w.WriteStringField(9, "tid-1")
	w.WriteVarintField(10, 1754668800)
	w.WriteStringField(18, "投喂")
	return w.Bytes()
}

type sendGiftBroadcastTestOpts struct {
	uid                  int64
	uname                string
	face                 string
	nameColor            string
	guardLevel           int64
	items                [][]byte
	switchPresent        bool
	switchOn             bool
	unknownVarintField   int
	unknownFixed64Field  int
	unknownFixed32Field  int
	unknownBytesFieldNum int
	unknownBytesField    []byte
}

func buildSendGiftBroadcastPB(opts sendGiftBroadcastTestOpts) []byte {
	w := &protowire.Writer{}
	w.WriteVarintField(1, uint64(opts.uid))
	w.WriteStringField(2, opts.uname)
	if opts.face != "" {
		w.WriteStringField(3, opts.face)
	}
	if opts.nameColor != "" {
		w.WriteStringField(4, opts.nameColor)
	}
	if opts.guardLevel != 0 {
		w.WriteVarintField(5, uint64(opts.guardLevel))
	}
	for _, item := range opts.items {
		w.WriteMessageField(10, item)
	}
	if opts.switchPresent {
		w.WriteBoolField(11, opts.switchOn)
	}
	if opts.unknownVarintField != 0 {
		w.WriteVarintField(opts.unknownVarintField, 42)
	}
	if opts.unknownFixed64Field != 0 {
		w.WriteFixed64Field(opts.unknownFixed64Field, 0xDEADBEEFCAFEBABE)
	}
	if opts.unknownFixed32Field != 0 {
		w.WriteFixed32Field(opts.unknownFixed32Field, 0xCAFEBABE)
	}
	if len(opts.unknownBytesField) > 0 {
		w.WriteBytesField(opts.unknownBytesFieldNum, opts.unknownBytesField)
	}
	return w.Bytes()
}

func buildSendGiftV2JSON(pb []byte) []byte {
	b64 := base64.StdEncoding.EncodeToString(pb)
	return []byte(`{"cmd":"SEND_GIFT_V2","data":{"pb":"` + b64 + `","dmscore":10}}`)
}

func TestParseGiftV2SingleItem(t *testing.T) {
	item := buildGiftV2ItemMessage(1, "小花花", 2)
	pb := buildSendGiftBroadcastPB(sendGiftBroadcastTestOpts{
		uid:           456,
		uname:         "礼物用户",
		face:          "https://example.com/f.png",
		nameColor:     "#00D1F1",
		guardLevel:    2,
		items:         [][]byte{item},
		switchPresent: true,
		switchOn:      true,
	})
	gifts, err := parseGiftV2(buildSendGiftV2JSON(pb))
	if err != nil {
		t.Fatalf("parseGiftV2: %v", err)
	}
	if len(gifts) != 1 {
		t.Fatalf("len(gifts) = %d, want 1", len(gifts))
	}
	g := gifts[0]
	if g.UID != 456 || g.Uname != "礼物用户" || g.GiftName != "小花花" || g.Num != 2 {
		t.Errorf("gift fields: %+v", g)
	}
	if g.Face != "https://example.com/f.png" || g.NameColor != "#00D1F1" {
		t.Errorf("overlay fields: %+v", g)
	}
	if g.Price != 100 || g.TotalCoin != 200 || g.CoinType != "gold" || g.Action != "投喂" {
		t.Errorf("item fields: %+v", g)
	}
	if g.GuardLevel != 2 || g.Tid != "tid-1" || g.Timestamp != 1754668800 {
		t.Errorf("meta fields: %+v", g)
	}
}

func TestParseGiftV2MultipleItems(t *testing.T) {
	items := [][]byte{
		buildGiftV2ItemMessage(1, "A", 1),
		buildGiftV2ItemMessage(2, "B", 3),
		buildGiftV2ItemMessage(3, "C", 5),
	}
	pb := buildSendGiftBroadcastPB(sendGiftBroadcastTestOpts{
		uid:   1,
		uname: "sender",
		items: items,
	})
	gifts, err := parseGiftV2(buildSendGiftV2JSON(pb))
	if err != nil {
		t.Fatalf("parseGiftV2: %v", err)
	}
	if len(gifts) != 3 {
		t.Fatalf("len(gifts) = %d, want 3", len(gifts))
	}
	want := []struct {
		name string
		num  int64
	}{
		{"A", 1},
		{"B", 3},
		{"C", 5},
	}
	for i, w := range want {
		if gifts[i].GiftName != w.name || gifts[i].Num != w.num {
			t.Errorf("gift[%d] = %+v, want name=%s num=%d", i, gifts[i], w.name, w.num)
		}
		if gifts[i].UID != 1 || gifts[i].Uname != "sender" {
			t.Errorf("gift[%d] sender = %+v", i, gifts[i])
		}
	}
}

func TestParseGiftV2SkipsUnknownWireTypes(t *testing.T) {
	item := buildGiftV2ItemMessage(1, "小花花", 1)
	pb := buildSendGiftBroadcastPB(sendGiftBroadcastTestOpts{
		uid:                  1,
		uname:                "u",
		items:                [][]byte{item},
		unknownVarintField:   99,
		unknownFixed64Field:  100,
		unknownFixed32Field:  101,
		unknownBytesFieldNum: 102,
		unknownBytesField:    []byte("skip-me"),
	})
	gifts, err := parseGiftV2(buildSendGiftV2JSON(pb))
	if err != nil {
		t.Fatalf("parseGiftV2: %v", err)
	}
	if len(gifts) != 1 || gifts[0].GiftName != "小花花" {
		t.Errorf("gifts: %+v", gifts)
	}
}

func TestParseGiftV2AnonymousSender(t *testing.T) {
	item := buildGiftV2ItemMessage(1, "小花花", 1)
	pb := buildSendGiftBroadcastPB(sendGiftBroadcastTestOpts{
		uid:   0,
		uname: "",
		items: [][]byte{item},
	})
	gifts, err := parseGiftV2(buildSendGiftV2JSON(pb))
	if err != nil {
		t.Fatalf("parseGiftV2: %v", err)
	}
	if len(gifts) != 1 {
		t.Fatalf("len(gifts) = %d, want 1", len(gifts))
	}
	if gifts[0].UID != 0 || gifts[0].Uname != "" {
		t.Errorf("anonymous sender: %+v", gifts[0])
	}
}

func TestParseGiftV2SwitchFalse(t *testing.T) {
	item := buildGiftV2ItemMessage(1, "小花花", 1)
	pb := buildSendGiftBroadcastPB(sendGiftBroadcastTestOpts{
		uid:           1,
		uname:         "u",
		items:         [][]byte{item},
		switchPresent: true,
		switchOn:      false,
	})
	gifts, err := parseGiftV2(buildSendGiftV2JSON(pb))
	if err != nil {
		t.Fatalf("parseGiftV2: %v", err)
	}
	if len(gifts) != 0 {
		t.Errorf("expected empty slice, got %+v", gifts)
	}
}

func TestParseGiftV2SwitchMissing(t *testing.T) {
	item := buildGiftV2ItemMessage(1, "小花花", 1)
	pb := buildSendGiftBroadcastPB(sendGiftBroadcastTestOpts{
		uid:   1,
		uname: "u",
		items: [][]byte{item},
	})
	gifts, err := parseGiftV2(buildSendGiftV2JSON(pb))
	if err != nil {
		t.Fatalf("parseGiftV2: %v", err)
	}
	if len(gifts) != 1 {
		t.Errorf("missing switch should still parse, got %+v", gifts)
	}
}

func TestParseGiftV2MissingPB(t *testing.T) {
	gifts, err := parseGiftV2([]byte(`{"cmd":"SEND_GIFT_V2","data":{"dmscore":1}}`))
	if err != nil {
		t.Fatalf("parseGiftV2: %v", err)
	}
	if len(gifts) != 0 {
		t.Errorf("expected empty slice, got %+v", gifts)
	}
}

func TestParseGiftV2EmptyGiftList(t *testing.T) {
	pb := buildSendGiftBroadcastPB(sendGiftBroadcastTestOpts{uid: 1, uname: "u"})
	gifts, err := parseGiftV2(buildSendGiftV2JSON(pb))
	if err != nil {
		t.Fatalf("parseGiftV2: %v", err)
	}
	if len(gifts) != 0 {
		t.Errorf("expected empty slice, got %+v", gifts)
	}
}

func TestParseGiftV2BadBase64(t *testing.T) {
	_, err := parseGiftV2([]byte(`{"cmd":"SEND_GIFT_V2","data":{"pb":"!!!"}}`))
	if err == nil {
		t.Fatal("expected base64 error")
	}
	if !strings.Contains(err.Error(), "base64") {
		t.Errorf("error = %v", err)
	}
}

func TestParseGiftV2TruncatedProtobuf(t *testing.T) {
	malformed := []byte{0x12, 0x05, 'h', 'e', 'l'}
	b64 := base64.StdEncoding.EncodeToString(malformed)
	raw := []byte(`{"cmd":"SEND_GIFT_V2","data":{"pb":"` + b64 + `"}}`)
	_, err := parseGiftV2(raw)
	if err == nil {
		t.Fatal("expected protobuf parse error")
	}
}

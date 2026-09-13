package danmaku

import (
	"encoding/base64"
	"fmt"

	"github.com/bilirec/bilirec/pkg/protowire"
	"github.com/tidwall/gjson"
)

type sendGiftBroadcast struct {
	uid           int64
	uname         string
	face          string
	nameColor     string
	guardLevel    int64
	giftList      []sendGiftV2Item
	switchPresent bool
	switchOn      bool
}

type sendGiftV2Item struct {
	giftID    int64
	giftName  string
	num       int64
	giftType  int64
	price     int64
	totalCoin int64
	coinType  string
	tid       string
	timestamp int64
	action    string
}

func parseGiftV2(raw []byte) ([]Gift, error) {
	pbB64 := gjson.GetBytes(raw, "data.pb").String()
	if pbB64 == "" {
		return nil, nil
	}
	pbData, err := base64.StdEncoding.DecodeString(pbB64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	broadcast, ok := parseSendGiftBroadcast(pbData)
	if !ok {
		return nil, fmt.Errorf("protobuf parse failed")
	}
	if broadcast.switchPresent && !broadcast.switchOn {
		return nil, nil
	}
	if len(broadcast.giftList) == 0 {
		return nil, nil
	}
	gifts := make([]Gift, 0, len(broadcast.giftList))
	for _, item := range broadcast.giftList {
		gifts = append(gifts, Gift{
			UID:        broadcast.uid,
			Uname:      broadcast.uname,
			Face:       broadcast.face,
			NameColor:  broadcast.nameColor,
			GuardLevel: broadcast.guardLevel,
			GiftID:     item.giftID,
			GiftName:   item.giftName,
			Num:        item.num,
			GiftType:   item.giftType,
			Price:      item.price,
			TotalCoin:  item.totalCoin,
			CoinType:   item.coinType,
			Tid:        item.tid,
			Timestamp:  item.timestamp,
			Action:     item.action,
		})
	}
	return gifts, nil
}

func parseSendGiftBroadcast(data []byte) (sendGiftBroadcast, bool) {
	var out sendGiftBroadcast
	r := protowire.NewReader(data)
	for {
		fieldNum, wireType, ok := r.TryReadTag()
		if !ok {
			break
		}
		if !applySendGiftBroadcastField(&out, r, fieldNum, wireType) {
			return sendGiftBroadcast{}, false
		}
	}
	return out, true
}

func applySendGiftBroadcastField(out *sendGiftBroadcast, r *protowire.Reader, fieldNum, wireType int) bool {
	switch fieldNum {
	case 1:
		return wireInt64(r, &out.uid)
	case 2:
		return wireString(r, &out.uname)
	case 3:
		return wireString(r, &out.face)
	case 4:
		return wireString(r, &out.nameColor)
	case 5:
		return wireInt64(r, &out.guardLevel)
	case 10:
		msg, ok := r.ReadBytes()
		if !ok {
			return false
		}
		item, ok := parseSendGiftV2Item(msg)
		if !ok {
			return false
		}
		out.giftList = append(out.giftList, item)
		return true
	case 11:
		v, ok := r.ReadBool()
		if !ok {
			return false
		}
		out.switchPresent = true
		out.switchOn = v
		return true
	default:
		return r.SkipField(wireType)
	}
}

func parseSendGiftV2Item(data []byte) (sendGiftV2Item, bool) {
	var out sendGiftV2Item
	r := protowire.NewReader(data)
	for {
		fieldNum, wireType, ok := r.TryReadTag()
		if !ok {
			break
		}
		if !applySendGiftV2ItemField(&out, r, fieldNum, wireType) {
			return sendGiftV2Item{}, false
		}
	}
	return out, true
}

func applySendGiftV2ItemField(out *sendGiftV2Item, r *protowire.Reader, fieldNum, wireType int) bool {
	switch fieldNum {
	case 1:
		return wireInt64(r, &out.giftID)
	case 2:
		return wireString(r, &out.giftName)
	case 3:
		return wireInt64(r, &out.num)
	case 4:
		return wireInt64(r, &out.giftType)
	case 5:
		return wireInt64(r, &out.price)
	case 7:
		return wireInt64(r, &out.totalCoin)
	case 8:
		return wireString(r, &out.coinType)
	case 9:
		return wireString(r, &out.tid)
	case 10:
		return wireInt64(r, &out.timestamp)
	case 18:
		return wireString(r, &out.action)
	default:
		return r.SkipField(wireType)
	}
}

func wireInt64(r *protowire.Reader, dst *int64) bool {
	v, ok := r.ReadInt64()
	if !ok {
		return false
	}
	*dst = v
	return true
}

func wireString(r *protowire.Reader, dst *string) bool {
	s, ok := r.ReadString()
	if !ok {
		return false
	}
	*dst = s
	return true
}

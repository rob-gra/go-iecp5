package asdu

import (
	"encoding/binary"
	"math"
	"time"
)

func (this *ASDU) AppendBytes(b ...byte) *ASDU {
	this.infoObj = append(this.infoObj, b...)
	return this
}
func (this *ASDU) DecodeByte() byte {
	v := this.infoObj[0]
	this.infoObj = this.infoObj[1:]
	return v
}
func (this *ASDU) AppendInfoObjAddr(addr InfoObjAddr) error {
	switch this.InfoObjAddrSize {
	case 1:
		if addr > 255 {
			return ErrInfoObjAddrFit
		}
		this.infoObj = append(this.infoObj, byte(addr))
	case 2:
		if addr > 65535 {
			return ErrInfoObjAddrFit
		}
		this.infoObj = append(this.infoObj, byte(addr), byte(addr>>8))
	case 3:
		if addr > 16777215 {
			return ErrInfoObjAddrFit
		}
		this.infoObj = append(this.infoObj, byte(addr), byte(addr>>8), byte(addr>>16))
	default:
		return ErrParam
	}
	return nil
}

func (this *ASDU) DecodeInfoObjAddr() InfoObjAddr {
	var ioa InfoObjAddr
	switch this.InfoObjAddrSize {
	case 1:
		ioa = InfoObjAddr(this.infoObj[0])
		this.infoObj = this.infoObj[1:]
	case 2:
		ioa = InfoObjAddr(this.infoObj[0]) | (InfoObjAddr(this.infoObj[1]) << 8)
		this.infoObj = this.infoObj[2:]
	case 3:
		ioa = InfoObjAddr(this.infoObj[0]) | (InfoObjAddr(this.infoObj[1]) << 8) | (InfoObjAddr(this.infoObj[2]) << 16)
		this.infoObj = this.infoObj[3:]
	default:
		panic(ErrParam)
	}
	return ioa
}

func (this *ASDU) AppendNormalize(n Normalize) *ASDU {
	this.infoObj = append(this.infoObj, byte(n), byte(n>>8))
	return this
}

func (this *ASDU) DecodeNormalize() Normalize {
	n := Normalize(binary.LittleEndian.Uint16(this.infoObj))
	this.infoObj = this.infoObj[2:]
	return n
}

func (this *ASDU) AppendScaled(i int16) *ASDU {
	this.infoObj = append(this.infoObj, byte(i), byte(i>>8))
	return this
}

func (this *ASDU) DecodeScaled() int16 {
	s := int16(binary.LittleEndian.Uint16(this.infoObj))
	this.infoObj = this.infoObj[2:]
	return s
}

func (this *ASDU) AppendFloat32(f float32) *ASDU {
	bits := math.Float32bits(f)
	this.infoObj = append(this.infoObj, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
	return this
}

func (this *ASDU) DecodeFloat() float32 {
	f := math.Float32frombits(binary.LittleEndian.Uint32(this.infoObj))
	this.infoObj = this.infoObj[4:]
	return f
}

func (this *ASDU) AppendBitsString32(v uint32) *ASDU {
	this.infoObj = append(this.infoObj, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
	return this
}

func (this *ASDU) DecodeBitsString32() uint32 {
	v := binary.LittleEndian.Uint32(this.infoObj)
	this.infoObj = this.infoObj[4:]
	return v
}

func (this *ASDU) DecodeCP56Time2a() time.Time {
	t := ParseCP56Time2a(this.infoObj, this.Params.InfoObjTimeZone)
	this.infoObj = this.infoObj[7:]
	return t
}

func (this *ASDU) DecodeCP24Time2a() time.Time {
	t := ParseCP24Time2a(this.infoObj, this.Params.InfoObjTimeZone)
	this.infoObj = this.infoObj[3:]
	return t
}
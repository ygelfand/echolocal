package bluetooth

import (
	"encoding/binary"
)

const (
	flagsDataType        = 0x01
	generalDiscoverable  = 0x02
	brEDRNotSupported    = 0x04
	manufacturerDataType = 0xff
	appleCompanyID       = 0x004c
	iBeaconType          = 0x02
	iBeaconPayloadLength = 0x15
	iBeaconMajor         = 1
	iBeaconMeasuredPower = -59
)

// This randomly assigned UUID identifies EchoLocal receivers. BPS distinguishes individual
// receivers by the complete UUID, major, and minor tuple.
var iBeaconUUID = [16]byte{
	0x9c, 0x5f, 0xa6, 0xf1, 0x91, 0xc4, 0x4f, 0x56,
	0xbb, 0x9f, 0xd9, 0x2a, 0xcf, 0xd9, 0xd4, 0x0b,
}

// beaconAdvertisement formats an iBeacon advertisement for minor.
func beaconAdvertisement(minor int) []byte {
	if minor < 0 || minor > 0xffff {
		return nil
	}

	advertisement := []byte{
		0x02, flagsDataType, generalDiscoverable | brEDRNotSupported,
	}
	manufacturerLength := len(advertisement)
	advertisement = append(advertisement, 0, manufacturerDataType)
	advertisement = binary.LittleEndian.AppendUint16(advertisement, appleCompanyID)
	advertisement = append(advertisement, iBeaconType, iBeaconPayloadLength)
	advertisement = append(advertisement, iBeaconUUID[:]...)
	advertisement = binary.BigEndian.AppendUint16(advertisement, iBeaconMajor)
	advertisement = binary.BigEndian.AppendUint16(advertisement, uint16(minor))
	measuredPower := int8(iBeaconMeasuredPower)
	advertisement = append(advertisement, byte(measuredPower))
	advertisement[manufacturerLength] = byte(len(advertisement) - manufacturerLength - 1)
	return advertisement
}

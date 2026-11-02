package protocol

import (
	"math"
	"time"
)

// FileTime is the NFS wire time format
// This is equivalent to go-nfs-client/nfs.NFS3Time
type FileTime struct {
	Seconds  uint32
	Nseconds uint32
}

// ToNFSTime generates the nfs 64bit time format from a golang time.
//
// nfstime3 counts seconds since the epoch in a uint32, so it reaches from 1970
// to 2106 and no further.  A time outside that is clamped rather than wrapped,
// because a wrapped one reads as a real date.
func ToNFSTime(t time.Time) FileTime {
	seconds := t.Unix()
	if seconds < 0 {
		return FileTime{}
	}
	if seconds > math.MaxUint32 {
		return FileTime{Seconds: math.MaxUint32, Nseconds: nsPerSecond - 1}
	}
	return FileTime{
		Seconds: uint32(seconds),
		// Nanosecond is the offset within the second, so it is always in
		// range.  UnixNano would be negative before 1970, and overflows
		// outside 1678 to 2262.
		Nseconds: uint32(t.Nanosecond()),
	}
}

// Native generates a golang time from an nfs time spec
func (t FileTime) Native() *time.Time {
	ts := time.Unix(int64(t.Seconds), int64(t.Nseconds))
	return &ts
}

// nsPerSecond is a second counted in the nanoseconds a time.Duration measures.
// It is untyped so that it serves both the int64 arithmetic below and the
// uint32 fields of a FileTime.
const nsPerSecond = 1_000_000_000

// durationToNFSTime converts a length of time into the nfstime3 that FSINFO
// reports a filesystem's timestamp precision as.
//
// nfstime3 counts whole seconds in a uint32, so it can hold neither a negative
// duration nor one beyond about 136 years, where a time.Duration reaches 292.
// Neither is a sensible precision for a filesystem to claim, and neither is
// worth failing a reply over, so they become zero and the longest nfstime3
// there is.
func durationToNFSTime(d time.Duration) FileTime {
	ns := d.Nanoseconds()
	if ns < 0 {
		return FileTime{}
	}
	seconds := ns / nsPerSecond
	if seconds > math.MaxUint32 {
		return FileTime{Seconds: math.MaxUint32, Nseconds: nsPerSecond - 1}
	}
	return FileTime{
		Seconds:  uint32(seconds),
		Nseconds: uint32(ns % nsPerSecond),
	}
}

// EqualTimespec returns if this time is equal to a local time spec
func (t FileTime) EqualTimespec(sec int64, nsec int64) bool {
	// TODO: bounds check on sec/nsec overflow
	return t.Nseconds == uint32(nsec) && t.Seconds == uint32(sec)
}

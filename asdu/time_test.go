package asdu

import (
	"reflect"
	"testing"
	"time"
)

var (
	tm0                = time.Date(2019, 6, 5, 4, 3, 0, 513000000, time.UTC)
	tm0CP56Time2aBytes = []byte{0x01, 0x02, 0x03, 0x04, 0x65, 0x06, 0x13}
	tm0CP24Time2aBytes = tm0CP56Time2aBytes[:3]

	// 15 Dec 2019 was a Sunday, so octet 5 carries day-of-week 7 (0xe0) with
	// day-of-month 15 (0x0f). This vector used to read 0x0f -- day-of-week 0,
	// which the standard defines as "not used" -- because Go numbers Sunday 0
	// and the encoder passed that through.
	tm1                = time.Date(2019, 12, 15, 14, 13, 3, 83000000, time.UTC)
	tm1CP56Time2aBytes = []byte{0x0b, 0x0c, 0x0d, 0x0e, 0xef, 0x0c, 0x13}
	tm1CP24Time2aBytes = tm1CP56Time2aBytes[:3]
)

func TestCP56Time2a(t *testing.T) {
	type args struct {
		t   time.Time
		loc *time.Location
	}
	tests := []struct {
		name string
		args args
		want []byte
	}{
		{"20190605", args{tm0, nil}, tm0CP56Time2aBytes},
		{"20191215", args{tm1, time.UTC}, tm1CP56Time2aBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CP56Time2a(tt.args.t, tt.args.loc); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CP56Time2a() = % x, want % x", got, tt.want)
			}
		})
	}
}

func TestParseCP56Time2a(t *testing.T) {
	type args struct {
		bytes []byte
		loc   *time.Location
	}
	tests := []struct {
		name string
		args args
		want time.Time
	}{
		{
			"invalid flag", args{
				[]byte{0x01, 0x02, 0x83, 0x04, 0x65, 0x06, 0x13},
				nil},
			time.Time{},
		},
		{"20190605", args{tm0CP56Time2aBytes, nil}, tm0},
		{"20191215", args{tm1CP56Time2aBytes, time.UTC}, tm1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseCP56Time2a(tt.args.bytes, tt.args.loc)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseCP56Time2a() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCP24Time2a(t *testing.T) {
	type args struct {
		t   time.Time
		loc *time.Location
	}
	tests := []struct {
		name string
		args args
		want []byte
	}{
		{"3 Minutes 513 Milliseconds", args{tm0, nil}, tm0CP24Time2aBytes},
		{"13 Minutes 3083 Milliseconds", args{tm1, time.UTC}, tm1CP24Time2aBytes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CP24Time2a(tt.args.t, tt.args.loc); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CP24Time2a() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCP24Time2a(t *testing.T) {
	type args struct {
		bytes []byte
		loc   *time.Location
	}
	tests := []struct {
		name     string
		args     args
		wantMsec int
		wantMin  int
	}{
		{
			"invalid flag",
			args{[]byte{0x01, 0x02, 0x83}, nil},
			0,
			0,
		},
		{
			"3 Minutes 513 Milliseconds",
			args{tm0CP24Time2aBytes, nil},
			513,
			3,
		},
		{
			"13 Minutes 3083 Milliseconds",
			args{tm1CP24Time2aBytes, time.UTC},
			3083,
			13,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseCP24Time2a(tt.args.bytes, tt.args.loc)
			msec := (got.Nanosecond()/int(time.Millisecond) + got.Second()*1000)
			if msec != tt.wantMsec {
				t.Errorf("ParseCP24Time2a() go Millisecond = %v, want %v", msec, tt.wantMsec)
			}
			if got.Minute() != tt.wantMin {
				t.Errorf("ParseCP24Time2a() got Minute = %v, want %v", got.Minute(), tt.wantMin)
			}
		})
	}
}

func TestCP16Time2a(t *testing.T) {
	type args struct {
		msec uint16
	}
	tests := []struct {
		name string
		args args
		want []byte
	}{
		{"513 Milliseconds", args{513}, []byte{0x01, 0x02}},
		{"3083 Milliseconds", args{3083}, []byte{0x0b, 0x0c}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CP16Time2a(tt.args.msec); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CP16Time2a() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCP16Time2a(t *testing.T) {
	type args struct {
		b []byte
	}
	tests := []struct {
		name string
		args args
		want uint16
	}{
		{"513 Milliseconds", args{[]byte{0x01, 0x02}}, 513},
		{"3083 Milliseconds", args{[]byte{0x0b, 0x0c}}, 3083},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseCP16Time2a(tt.args.b); got != tt.want {
				t.Errorf("ParseCP16Time2a() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Day of week is 1=Monday through 7=Sunday (subclass 7.2.6.18), not Go's
// 0=Sunday through 6=Saturday. The two agree for Monday through Saturday,
// which is why only Sunday ever showed the difference.
func TestCP56Time2a_DayOfWeek(t *testing.T) {
	// 4 Mar 2024 was a Monday.
	for i := 0; i < 7; i++ {
		day := time.Date(2024, 3, 4+i, 12, 0, 0, 0, time.UTC)
		want := int(day.Weekday())
		if want == 0 {
			want = 7
		}
		got := int(CP56Time2a(day, time.UTC)[4] >> 5)
		if got != want {
			t.Errorf("%s: day of week = %d, want %d", day.Weekday(), got, want)
		}
		if gotDay := int(CP56Time2a(day, time.UTC)[4] & 0x1f); gotDay != day.Day() {
			t.Errorf("%s: day of month = %d, want %d", day.Weekday(), gotDay, day.Day())
		}
	}
}

// The SU bit marks a summer-time value. Left clear, a summer local time reads
// as standard time an hour earlier.
func TestCP56Time2a_SummerTimeBit(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	summer := time.Date(2024, 7, 1, 12, 0, 0, 0, berlin) // CEST, DST in effect
	winter := time.Date(2024, 1, 1, 12, 0, 0, 0, berlin) // CET, no DST

	if su := CP56Time2a(summer, berlin)[3] & 0x80; su == 0 {
		t.Error("SU clear for a summer-time value")
	}
	if su := CP56Time2a(winter, berlin)[3] & 0x80; su != 0 {
		t.Error("SU set for a standard-time value")
	}
	// UTC never observes DST, so the bit must stay clear there.
	if su := CP56Time2a(summer, time.UTC)[3] & 0x80; su != 0 {
		t.Error("SU set when encoding in UTC")
	}
}

// CP24Time2a carries only milliseconds, seconds and minutes. Decoding must
// therefore be a pure function of the three octets: it used to complete the
// date and hour from time.Now(), which made the same bytes decode differently
// depending on when they were read.
func TestParseCP24Time2a_IsDeterministic(t *testing.T) {
	b := []byte{0x0b, 0x0c, 0x0d} // 13 minutes, 3083 ms

	first := ParseCP24Time2a(b, time.UTC)
	second := ParseCP24Time2a(b, time.UTC)
	if !first.Equal(second) {
		t.Fatalf("same bytes decoded to %v then %v", first, second)
	}

	// Only the fields the encoding carries are set.
	if first.Minute() != 13 || first.Second() != 3 || first.Nanosecond()/int(time.Millisecond) != 83 {
		t.Fatalf("got %02d:%02d.%03d, want 13 minutes 3.083 s",
			first.Minute(), first.Second(), first.Nanosecond()/int(time.Millisecond))
	}
	// Nothing invented for the parts it does not carry.
	if first.Year() != 1 || first.Month() != time.January || first.Day() != 1 || first.Hour() != 0 {
		t.Fatalf("decoding invented a date/hour: %v", first)
	}
}

// The year field is seven bits holding 0-99, so it reduces modulo 100.
// `Year() - 2000` went negative before 2000 and overflowed the field from
// 2100, in both cases writing a year that was never asked for -- and, past
// 2127, spilling into the reserved bit.
func TestCP56Time2a_YearWrapsWithinTheField(t *testing.T) {
	for _, tc := range []struct {
		year int
		want byte
	}{
		{2000, 0}, {2019, 19}, {2099, 99}, {2100, 0}, {2145, 45}, {1999, 99},
	} {
		ts := time.Date(tc.year, 6, 1, 12, 0, 0, 0, time.UTC)
		got := CP56Time2a(ts, time.UTC)[6]
		if got&0x80 != 0 {
			t.Errorf("year %d: reserved bit 7 set (%#02x)", tc.year, got)
		}
		if got != tc.want {
			t.Errorf("year %d: encoded %d, want %d", tc.year, got, tc.want)
		}
	}
}

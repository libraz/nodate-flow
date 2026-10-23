package dbtype

// The range a MySQL DATETIME column holds, 1000-01-01T00:00:00Z through
// 9999-12-31T23:59:59Z, in unix seconds. A value outside it fails the
// write in STRICT mode, so a unix-seconds input is checked against it
// before it reaches a column.
const (
	MinDatetimeUnix int64 = -30610224000
	MaxDatetimeUnix int64 = 253402300799
)

// DatetimeRepresentable reports whether unix seconds s fits a DATETIME
// column.
func DatetimeRepresentable(s int64) bool {
	return s >= MinDatetimeUnix && s <= MaxDatetimeUnix
}

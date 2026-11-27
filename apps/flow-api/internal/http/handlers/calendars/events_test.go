package calendars

import "testing"

func TestRequireNotificationOffset(t *testing.T) {
	t.Parallel()

	in := func(v int32) *int32 { return &v }
	for _, ok := range []*int32{nil, in(0), in(10080)} {
		if err := requireNotificationOffset(ok); err != nil {
			t.Errorf("%v must pass: %v", ok, err)
		}
	}
	for _, bad := range []int32{-1, 10081} {
		err := requireNotificationOffset(in(bad))
		if got := problemCode(t, err); got != "CALENDAR.EVENT.NOTIFICATION_OFFSET_OUT_OF_RANGE" {
			t.Errorf("%d: got %s", bad, got)
		}
	}
}

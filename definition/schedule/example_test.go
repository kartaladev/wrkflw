package schedule_test

import (
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/schedule"
)

func ExampleAfterDuration() {
	d, _ := schedule.AfterDuration(90 * time.Minute).Duration()
	fmt.Println(d)
	// Output: 1h30m0s
}

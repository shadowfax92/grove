package footprint

import (
	"golang.org/x/sys/unix"
	"time"
)

func statModTime(st *unix.Stat_t) time.Time { return time.Unix(st.Mtim.Sec, st.Mtim.Nsec) }

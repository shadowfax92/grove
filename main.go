package main

// Import time/tzdata to embed the IANA timezone database in the binary, so the
// Pacific-time stamps in auto-generated branch names resolve even on machines
// without system zoneinfo (minimal containers, some CI images).
import (
	_ "time/tzdata"

	"grove/cmd"
)

func main() {
	cmd.Execute()
}

package footprint

func openDirectoryAt(rootFD int, path string) (int, error) {
	return openDirectoryComponents(rootFD, path)
}

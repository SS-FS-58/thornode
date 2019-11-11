package thorchain

func getVersion(sversion int64, prefix dbPrefix) int64 {
	switch prefix {
	case prefixNodeAccount:
		return getNodeAccountVersion(sversion)
	default:
		return 1 // default
	}
}

func getNodeAccountVersion(sversion int64) int64 {
	return 1 // default
}
package tool

// SummaryLimit bounds approval summaries, allowing 16 KiB of framing beyond
// the argument limit and retaining a 64 KiB minimum for small write tools.
func (r ConfirmRequest) SummaryLimit() int {
	limit := r.MaxArgBytes
	if limit <= 0 {
		limit = maxArgumentBytes
	}
	const maxInt = int(^uint(0) >> 1)
	if limit > maxInt-(16<<10) {
		return maxInt
	}
	return max(64<<10, limit+(16<<10))
}

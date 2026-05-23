package tag

func SplitBatches(items []BatchItem, batchSize int) [][]BatchItem {
	var batches [][]BatchItem
	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}
		batches = append(batches, items[i:end])
	}
	return batches
}

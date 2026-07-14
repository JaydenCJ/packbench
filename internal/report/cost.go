package report

// Cost projection. Ratios are measured on the sample and extrapolated
// onto the full scanned corpus, because the point of sampling is to
// price the whole bucket without reading the whole bucket. Prices are
// per GiB-month: storage vendors bill binary gigabytes even when the
// price sheet says "GB".

const bytesPerGiB = float64(1 << 30)

// monthlyUSD prices a byte count at pricePerGiBMonth.
func monthlyUSD(bytes float64, pricePerGiBMonth float64) float64 {
	if bytes < 0 {
		bytes = 0
	}
	return bytes / bytesPerGiB * pricePerGiBMonth
}

// projectedBytes extrapolates a sample ratio onto the full corpus size.
func projectedBytes(ratio float64, corpusBytes int64) float64 {
	return ratio * float64(corpusBytes)
}

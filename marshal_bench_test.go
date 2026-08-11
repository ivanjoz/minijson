package minijson_test

import (
	"fmt"
	"testing"

	"github.com/ivanjoz/minijson"
)

type benchmarkRecord struct {
	ID       int32    `json:"id"`
	Name     string   `json:"name"`
	Tags     []string `json:"tags,omitempty"`
	Quantity float64  `json:"quantity,omitempty"`
}

func BenchmarkMarshal(b *testing.B) {
	for _, recordCount := range []int{100, 1000, 5000} {
		records := make([]benchmarkRecord, recordCount)
		for recordIndex := range records {
			records[recordIndex] = benchmarkRecord{
				ID:       int32(recordIndex + 1),
				Name:     fmt.Sprintf("record-%d", recordIndex+1),
				Tags:     []string{"stock", "active"},
				Quantity: float64(recordIndex) / 10,
			}
		}

		encoded, marshalError := minijson.Marshal(&records)
		if marshalError != nil {
			b.Fatalf("Marshal returned error: %v", marshalError)
		}

		b.Run(fmt.Sprintf("%d", recordCount), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(encoded)))
			for b.Loop() {
				if _, marshalError := minijson.Marshal(&records); marshalError != nil {
					b.Fatalf("Marshal returned error: %v", marshalError)
				}
			}
		})
	}
}

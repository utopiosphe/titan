package httpserver

import (
	"net/http"
	"time"
)

type SpeedCountWriter struct {
	w         http.ResponseWriter
	dataSize  int64
	startTime time.Time
}

func (w *SpeedCountWriter) Header() http.Header {
	return w.w.Header()
}

func (w *SpeedCountWriter) Write(bytes []byte) (int, error) {
	if w.startTime.IsZero() {
		w.startTime = time.Now()
	}

	w.dataSize += int64(len(bytes))
	return w.w.Write(bytes)
}

func (w *SpeedCountWriter) WriteHeader(statusCode int) {
	w.w.WriteHeader(statusCode)
}

// ms
func (w *SpeedCountWriter) CostTime() float64 {
	return float64(time.Since(w.startTime)) / float64(time.Millisecond)
}

func (w *SpeedCountWriter) speed() int64 {
	if w.dataSize == 0 {
		return 0
	}

	speed := float64(0)
	duration := time.Since(w.startTime)
	if duration > 0 {
		speed = float64(w.dataSize) / float64(duration) * float64(time.Second)
	}

	return int64(speed)
}

package hooks

import "time"

const (
	EventFileAvailable     = "file.available"
	EventDownloadStarted   = "download.started"
	EventDownloadCompleted = "download.completed"
	EventDownloadFailed    = "download.failed"
	EventDownloadCancelled = "download.cancelled"
	EventChecksumMismatch  = "checksum.mismatch"
	EventSyncCompleted     = "sync.completed"
	EventSyncFailed        = "sync.failed"
)

// Event represents a hook event
type Event struct {
	Type      string    `json:"event"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
	Product   *Product  `json:"product,omitempty"`
	Delivery  *Delivery `json:"delivery,omitempty"`
	File      *File     `json:"file,omitempty"`
	Alerts    []Alert   `json:"alerts,omitempty"`
	Error     *Error    `json:"error,omitempty"`
}

// Product info for event payload
type Product struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Delivery info for event payload
type Delivery struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// File info for event payload
type File struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Checksum string `json:"checksum,omitempty"`
	Path     string `json:"path,omitempty"`
}

// Alert represents an alert in the event payload
type Alert struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // "info", "warning", "error"
}

// Error represents an error in the event payload
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewEvent creates a new event with the current timestamp
func NewEvent(eventType, source string) *Event {
	return &Event{
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Source:    source,
		Alerts:    []Alert{},
	}
}

// WithProduct sets the product info
func (e *Event) WithProduct(id, name string) *Event {
	e.Product = &Product{ID: id, Name: name}
	return e
}

// WithDelivery sets the delivery info
func (e *Event) WithDelivery(id, name string) *Event {
	e.Delivery = &Delivery{ID: id, Name: name}
	return e
}

// WithFile sets the file info
func (e *Event) WithFile(id, name string, size int64, checksum, path string) *Event {
	e.File = &File{
		ID:       id,
		Name:     name,
		Size:     size,
		Checksum: checksum,
		Path:     path,
	}
	return e
}

// WithAlert adds an alert
func (e *Event) WithAlert(alertType, message, severity string) *Event {
	e.Alerts = append(e.Alerts, Alert{
		Type:     alertType,
		Message:  message,
		Severity: severity,
	})
	return e
}

// WithError sets the error info
func (e *Event) WithError(code, message string) *Event {
	e.Error = &Error{Code: code, Message: message}
	return e
}

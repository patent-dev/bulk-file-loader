package database

import "time"

type Source struct {
	ID             string `gorm:"primaryKey"`
	Name           string
	Enabled        bool `gorm:"default:false"`
	CredentialsEnc []byte
	LastSyncAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Product struct {
	ID               string `gorm:"primaryKey"`
	SourceID         string `gorm:"index"`
	ExternalID       string
	Name             string
	Description      string
	AutoDownload     bool `gorm:"default:false"`
	CheckWindowStart string
	CheckWindowEnd   string
	LastCheckedAt    *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time

	Source     Source     `gorm:"foreignKey:SourceID"`
	Deliveries []Delivery `gorm:"foreignKey:ProductID"`
}

type Delivery struct {
	ID          string `gorm:"primaryKey"`
	ProductID   string `gorm:"index"`
	ExternalID  string
	Name        string
	PublishedAt *time.Time
	ExpiresAt   *time.Time
	CreatedAt   time.Time

	Product Product `gorm:"foreignKey:ProductID"`
	Files   []File  `gorm:"foreignKey:DeliveryID"`
}

type File struct {
	ID                string `gorm:"primaryKey"`
	DeliveryID        string `gorm:"index"`
	ProductID         string `gorm:"index"`
	SourceID          string `gorm:"index"`
	ExternalID        string
	FileName          string
	FileSize          int64
	ExpectedChecksum  string
	ChecksumAlgorithm string
	DownloadURI       string
	ReleasedAt        *time.Time
	Skipped           bool `gorm:"default:false"`
	CreatedAt         time.Time
	UpdatedAt         time.Time

	Delivery        Delivery        `gorm:"foreignKey:DeliveryID"`
	DownloadEntries []DownloadEntry `gorm:"foreignKey:FileID"`
}

type DownloadEntry struct {
	ID            uint   `gorm:"primaryKey"`
	FileID        string `gorm:"index"`
	Status        string
	Progress      int64
	TotalBytes    int64
	LocalPath     string
	LocalChecksum string
	ErrorMessage  string
	StartedAt     *time.Time
	CompletedAt   *time.Time
	CreatedAt     time.Time

	File File `gorm:"foreignKey:FileID"`
}

const (
	DownloadStatusPending     = "pending"
	DownloadStatusDownloading = "downloading"
	DownloadStatusCompleted   = "completed"
	DownloadStatusFailed      = "failed"
	DownloadStatusCancelled   = "cancelled"
)

// DownloadJob is a file waiting for, or in, a download attempt. A finished
// or cancelled download deletes its job; see docs/download-queue.md.
type DownloadJob struct {
	FileID          string `gorm:"primaryKey"`
	State           string `gorm:"index"`
	Attempts        int
	MaxAttempts     int
	NextAttemptAt   time.Time `gorm:"index"`
	LastError       string
	RequestedBy     string `gorm:"index"`
	ClaimedBy       string
	LeaseUntil      *time.Time
	CancelRequested bool `gorm:"default:false"`
	Version         int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const (
	JobQueued   = "queued"
	JobRunning  = "running"
	JobRetrying = "retrying"
	JobFailed   = "failed"
)

type Webhook struct {
	ID        uint `gorm:"primaryKey"`
	Name      string
	URL       string
	Events    string
	Headers   []byte
	Enabled   bool `gorm:"default:true"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Setting struct {
	Key   string `gorm:"primaryKey"`
	Value string
}

const (
	SettingPassphraseHash = "passphrase_hash"
	SettingPassphraseSalt = "passphrase_salt"
	SettingEncryptionSalt = "encryption_salt"
)

// OptionalTime returns nil for the zero time so it is stored as NULL (MySQL
// in strict mode rejects the zero date), and the time in UTC otherwise:
// SQLite stores times as text, so mixed offsets would not compare correctly.
func OptionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

package application

import (
	"errors"
	"fmt"
)

// SourceKind identifies the protocol class of a discovered resource.
type SourceKind string

const (
	SourcePT SourceKind = "pt"
	SourceBT SourceKind = "bt"
)

// DownloaderKind identifies a configured download client.
type DownloaderKind string

const (
	DownloaderAria2        DownloaderKind = "aria2"
	DownloaderQbittorrent  DownloaderKind = "qbittorrent"
	DownloaderTransmission DownloaderKind = "transmission"
	DownloaderThunder      DownloaderKind = "thunder"
	DownloaderCloudDrive2  DownloaderKind = "clouddrive2"
)

var ErrDownloaderNotAllowed = errors.New("downloader is not allowed for source")

var (
	ptDefaultDownloaders = []DownloaderKind{DownloaderQbittorrent, DownloaderTransmission}
	btDefaultDownloaders = []DownloaderKind{DownloaderQbittorrent, DownloaderTransmission, DownloaderAria2, DownloaderThunder}
)

// ValidateDownloader is the single policy used before a resource is submitted.
// PT resources must stay in qBittorrent/Transmission to preserve private-tracker
// announce semantics; BT resources may use any supported client.
func ValidateDownloader(source SourceKind, downloader DownloaderKind) error {
	if source != SourcePT && source != SourceBT {
		return fmt.Errorf("invalid source kind %q", source)
	}
	switch downloader {
	case DownloaderAria2, DownloaderQbittorrent, DownloaderTransmission, DownloaderThunder, DownloaderCloudDrive2:
	default:
		return fmt.Errorf("invalid downloader kind %q", downloader)
	}
	if source == SourcePT && downloader != DownloaderQbittorrent && downloader != DownloaderTransmission {
		return fmt.Errorf("%w: PT resources require qbittorrent or transmission", ErrDownloaderNotAllowed)
	}
	return nil
}

// ValidateDefaultDownloader validates the administrator-selected default client.
// CloudDrive2 deliberately remains available to legacy/manual flows but is not
// selectable as the automatic PT/BT default.
func ValidateDefaultDownloader(source SourceKind, downloader DownloaderKind) error {
	if source != SourcePT && source != SourceBT {
		return fmt.Errorf("invalid source kind %q", source)
	}
	allowed := ptDefaultDownloaders
	if source == SourceBT {
		allowed = btDefaultDownloaders
	}
	for _, candidate := range allowed {
		if candidate == downloader {
			return nil
		}
	}
	return fmt.Errorf("%w: %s default downloader must be one of the configured clients", ErrDownloaderNotAllowed, source)
}

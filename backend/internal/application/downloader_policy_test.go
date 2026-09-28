package application

import "testing"

func TestValidateDownloaderAllowsPTOnlyInManagedClients(t *testing.T) {
	for _, downloader := range []DownloaderKind{DownloaderQbittorrent, DownloaderTransmission} {
		if err := ValidateDownloader(SourcePT, downloader); err != nil {
			t.Fatalf("PT should allow %s: %v", downloader, err)
		}
	}
	for _, downloader := range []DownloaderKind{DownloaderAria2, DownloaderThunder, DownloaderCloudDrive2} {
		if err := ValidateDownloader(SourcePT, downloader); err == nil {
			t.Fatalf("PT should reject %s", downloader)
		}
	}
}

func TestValidateDownloaderAllowsBTWithAria2(t *testing.T) {
	if err := ValidateDownloader(SourceBT, DownloaderAria2); err != nil {
		t.Fatalf("BT should allow aria2: %v", err)
	}
}

func TestValidateDownloaderRejectsCloudDriveForBTDefaults(t *testing.T) {
	if err := ValidateDefaultDownloader(SourceBT, DownloaderCloudDrive2); err == nil {
		t.Fatal("BT default downloader must not allow CloudDrive2")
	}
}

func TestValidateDownloaderRejectsUnknownValues(t *testing.T) {
	if err := ValidateDownloader(SourceKind("other"), DownloaderQbittorrent); err == nil {
		t.Fatal("unknown source must be rejected")
	}
	if err := ValidateDownloader(SourceBT, DownloaderKind("other")); err == nil {
		t.Fatal("unknown downloader must be rejected")
	}
}

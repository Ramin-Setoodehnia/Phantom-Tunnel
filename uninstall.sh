package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const licenseServerURL = "https://license.xst.cl/index.php"

type LicenseStatusResponse struct {
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
	ExpiryDate    string `json:"expiry_date,omitempty"`
	DaysRemaining int    `json:"days_remaining,omitempty"`
}

func getDeviceID() (string, error) {
	id, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		log.Printf("Could not read /etc/machine-id: %v. Using hostname as fallback.", err)
		hostname, err_host := os.Hostname()
		if err_host != nil {
			return "", errors.New("could not get machine-id or hostname")
		}
		return hostname, nil
	}
	return strings.TrimSpace(string(id)), nil
}

func readLicenseKey() (string, error) {
	key, found := getSetting("license_key")
	if !found {
		return "", errors.New("license key not found in database")
	}
	return key, nil
}

func writeLicenseKey(key string) error {
	return setSetting("license_key", key)
}

func checkLicenseWithServer(key string) (*LicenseStatusResponse, error) {
	deviceID, err := getDeviceID()
	if err != nil {
		return nil, errors.New("could not get device ID")
	}

	formData := url.Values{
		"license_key": {key},
		"device_id":   {deviceID},
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.PostForm(licenseServerURL, formData)
	if err != nil {
		log.Printf("Internal license check error: %v", err)
		return nil, errors.New("failed to contact license server")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Internal license check error: server returned non-200 status: %s", resp.Status)
		return nil, errors.New("license server returned an invalid response")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Internal license check error: failed to read response body: %v", err)
		return nil, errors.New("failed to read response from license server")
	}

	var statusResp LicenseStatusResponse
	if err := json.Unmarshal(body, &statusResp); err != nil {
		log.Printf("Internal license check error: invalid JSON response: %s", string(body))
		return nil, errors.New("invalid response format from license server")
	}

	return &statusResp, nil
}

func ValidateLicense() (bool, error) {
	key, err := readLicenseKey()
	if err != nil {
		return false, err
	}

	status, err := checkLicenseWithServer(key)
	if err != nil {
		log.Printf("License check failed: %v", err)
		return false, errors.New("could not connect to the license server for validation")
	}

	if status.Status != "valid" {
		reason := status.Reason
		if reason == "" {
			reason = "Unknown reason"
		}
		return false, fmt.Errorf("license invalid: %s", reason)
	}

	return true, nil
}

func ActivateLicense(key string) error {
	if key == "" {
		return errors.New("license key cannot be empty")
	}

	status, err := checkLicenseWithServer(key)
	if err != nil {
		return fmt.Errorf("activation request failed: %w", err)
	}

	if status.Status != "valid" {
		reason := status.Reason
		if reason == "" {
			reason = "Unknown reason"
		}
		return fmt.Errorf("activation failed: %s", reason)
	}

	if err := writeLicenseKey(key); err != nil {
		return fmt.Errorf("failed to save license key file: %w", err)
	}

	return nil
}

func GetLicenseInfo() map[string]string {
	info := map[string]string{
		"status":  "Inactive",
		"message": "No license key found. Please activate.",
		"key":     "",
	}

	key, err := readLicenseKey()
	if err != nil {
		return info
	}

	if len(key) > 4 {
		info["key"] = "••••" + key[len(key)-4:]
	} else {
		info["key"] = "••••"
	}

	status, err := checkLicenseWithServer(key)
	if err != nil {
		info["status"] = "Inactive"
		info["message"] = "Error connecting to license server. Please check your internet connection."
		return info
	}

	if status.Status == "valid" {
		info["status"] = "Active"
		message := "Your license is active and valid for this device."
		if status.ExpiryDate != "" {
			message += fmt.Sprintf(" Expires on: %s", status.ExpiryDate)
		}
		if status.DaysRemaining >= 0 {
			message += fmt.Sprintf(" (%d days remaining).", status.DaysRemaining)
		}
		info["message"] = message

	} else {
		info["status"] = "Inactive"
		if status.Reason != "" {
			info["message"] = "License invalid: " + status.Reason
		} else {
			info["message"] = "License is invalid for an unknown reason."
		}
	}
	return info
}

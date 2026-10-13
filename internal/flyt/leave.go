package flyt

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// LeaveType is the application type for leave, see ApplicationTypes.
const LeaveType = 1

// What the web client lets a guardian put in an application for leave. The
// service may check the same things, but only the web client was read.
const (
	// MaxLeaveDays is how many days after the first day the last day can be.
	MaxLeaveDays = 60
	// MaxReasonLength is the longest reason, in characters.
	MaxReasonLength = 2000
	MaxAttachments  = 3
	// MaxAttachmentBytes is the largest file.
	MaxAttachmentBytes = 5 * 1024 * 1024
)

// AttachmentTypes is what a file on an application can hold, by what
// http.DetectContentType says about it.
var AttachmentTypes = []string{"application/pdf", "image/png", "image/jpeg"}

// ApplicationType is what a school says about one type of application.
type ApplicationType struct {
	Name string `json:"name"`
	Type int    `json:"type"`
	// HasRulesDocument says that the school has rules the guardian must
	// confirm having read.
	HasRulesDocument bool `json:"hasRulesDocument"`
	Template         struct {
		// ApplicationInfoText explains the causes. ArchiveText says how the
		// school keeps the application.
		ApplicationInfoText string `json:"applicationInfoText"`
		ArchiveText         string `json:"archiveText"`
	} `json:"template"`
}

// ApplicationTypeInfo returns what a school says about one type of application.
func (c *Client) ApplicationTypeInfo(ctx context.Context, school string, kind int) (*ApplicationType, error) {
	var info ApplicationType
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/schools/%s/applicationTypes/%d", school, kind), nil, &info)
	return &info, err
}

// ApplicationCause is one cause a guardian can pick for an application.
type ApplicationCause struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}

// ApplicationCauses lists the causes a school takes for one type of application.
func (c *Client) ApplicationCauses(ctx context.Context, school string, kind int) ([]ApplicationCause, error) {
	var causes []ApplicationCause
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("vfs/v1/schools/%s/applicationTypes/%d/causes", school, kind), nil, &causes)
	return causes, err
}

// LeaveRequest is what an application for leave sends, as the JSON in the
// form field "request". A dry run prints it.
type LeaveRequest struct {
	ApplicationType int    `json:"applicationType"`
	Comment         string `json:"comment"`
	CauseID         int    `json:"applicationTypeCauseId"`
	FromDate        string `json:"fromDate"`
	ToDate          string `json:"toDate"`
	// HasReadRules is null for a school without rules to confirm, as the
	// web client sends it.
	HasReadRules *bool `json:"hasReadRules"`
	PupilIDs     []int `json:"pupilInYearIds"`
}

// LeaveDate writes a day as the web client sends it.
func LeaveDate(day time.Time) string { return day.Format("2006-01-02") + "T00:00:00" }

// FormFile is a file sent along with a request.
type FormFile struct {
	Name        string
	ContentType string
	Data        []byte
}

// CreateApplication sends an application to a school. It is not safe to
// run again: the service has no key that tells a second try from a new
// application, so a caller that got no answer looks the application up
// before it sends once more.
func (c *Client) CreateApplication(ctx context.Context, school string, request LeaveRequest, files []FormFile) error {
	_, err := c.fetch(ctx, http.MethodPost, fmt.Sprintf("vfs/v1/schools/%s/applications", school), formRequest{Request: request, Files: files})
	return err
}

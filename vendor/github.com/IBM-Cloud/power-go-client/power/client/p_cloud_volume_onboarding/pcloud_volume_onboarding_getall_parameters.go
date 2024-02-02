// Code generated by go-swagger; DO NOT EDIT.

package p_cloud_volume_onboarding

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"context"
	"net/http"
	"time"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/runtime"
	cr "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
)

// NewPcloudVolumeOnboardingGetallParams creates a new PcloudVolumeOnboardingGetallParams object,
// with the default timeout for this client.
//
// Default values are not hydrated, since defaults are normally applied by the API server side.
//
// To enforce default values in parameter, use SetDefaults or WithDefaults.
func NewPcloudVolumeOnboardingGetallParams() *PcloudVolumeOnboardingGetallParams {
	return &PcloudVolumeOnboardingGetallParams{
		timeout: cr.DefaultTimeout,
	}
}

// NewPcloudVolumeOnboardingGetallParamsWithTimeout creates a new PcloudVolumeOnboardingGetallParams object
// with the ability to set a timeout on a request.
func NewPcloudVolumeOnboardingGetallParamsWithTimeout(timeout time.Duration) *PcloudVolumeOnboardingGetallParams {
	return &PcloudVolumeOnboardingGetallParams{
		timeout: timeout,
	}
}

// NewPcloudVolumeOnboardingGetallParamsWithContext creates a new PcloudVolumeOnboardingGetallParams object
// with the ability to set a context for a request.
func NewPcloudVolumeOnboardingGetallParamsWithContext(ctx context.Context) *PcloudVolumeOnboardingGetallParams {
	return &PcloudVolumeOnboardingGetallParams{
		Context: ctx,
	}
}

// NewPcloudVolumeOnboardingGetallParamsWithHTTPClient creates a new PcloudVolumeOnboardingGetallParams object
// with the ability to set a custom HTTPClient for a request.
func NewPcloudVolumeOnboardingGetallParamsWithHTTPClient(client *http.Client) *PcloudVolumeOnboardingGetallParams {
	return &PcloudVolumeOnboardingGetallParams{
		HTTPClient: client,
	}
}

/*
PcloudVolumeOnboardingGetallParams contains all the parameters to send to the API endpoint

	for the pcloud volume onboarding getall operation.

	Typically these are written to a http.Request.
*/
type PcloudVolumeOnboardingGetallParams struct {

	/* CloudInstanceID.

	   Cloud Instance ID of a PCloud Instance
	*/
	CloudInstanceID string

	timeout    time.Duration
	Context    context.Context
	HTTPClient *http.Client
}

// WithDefaults hydrates default values in the pcloud volume onboarding getall params (not the query body).
//
// All values with no default are reset to their zero value.
func (o *PcloudVolumeOnboardingGetallParams) WithDefaults() *PcloudVolumeOnboardingGetallParams {
	o.SetDefaults()
	return o
}

// SetDefaults hydrates default values in the pcloud volume onboarding getall params (not the query body).
//
// All values with no default are reset to their zero value.
func (o *PcloudVolumeOnboardingGetallParams) SetDefaults() {
	// no default values defined for this parameter
}

// WithTimeout adds the timeout to the pcloud volume onboarding getall params
func (o *PcloudVolumeOnboardingGetallParams) WithTimeout(timeout time.Duration) *PcloudVolumeOnboardingGetallParams {
	o.SetTimeout(timeout)
	return o
}

// SetTimeout adds the timeout to the pcloud volume onboarding getall params
func (o *PcloudVolumeOnboardingGetallParams) SetTimeout(timeout time.Duration) {
	o.timeout = timeout
}

// WithContext adds the context to the pcloud volume onboarding getall params
func (o *PcloudVolumeOnboardingGetallParams) WithContext(ctx context.Context) *PcloudVolumeOnboardingGetallParams {
	o.SetContext(ctx)
	return o
}

// SetContext adds the context to the pcloud volume onboarding getall params
func (o *PcloudVolumeOnboardingGetallParams) SetContext(ctx context.Context) {
	o.Context = ctx
}

// WithHTTPClient adds the HTTPClient to the pcloud volume onboarding getall params
func (o *PcloudVolumeOnboardingGetallParams) WithHTTPClient(client *http.Client) *PcloudVolumeOnboardingGetallParams {
	o.SetHTTPClient(client)
	return o
}

// SetHTTPClient adds the HTTPClient to the pcloud volume onboarding getall params
func (o *PcloudVolumeOnboardingGetallParams) SetHTTPClient(client *http.Client) {
	o.HTTPClient = client
}

// WithCloudInstanceID adds the cloudInstanceID to the pcloud volume onboarding getall params
func (o *PcloudVolumeOnboardingGetallParams) WithCloudInstanceID(cloudInstanceID string) *PcloudVolumeOnboardingGetallParams {
	o.SetCloudInstanceID(cloudInstanceID)
	return o
}

// SetCloudInstanceID adds the cloudInstanceId to the pcloud volume onboarding getall params
func (o *PcloudVolumeOnboardingGetallParams) SetCloudInstanceID(cloudInstanceID string) {
	o.CloudInstanceID = cloudInstanceID
}

// WriteToRequest writes these params to a swagger request
func (o *PcloudVolumeOnboardingGetallParams) WriteToRequest(r runtime.ClientRequest, reg strfmt.Registry) error {

	if err := r.SetTimeout(o.timeout); err != nil {
		return err
	}
	var res []error

	// path param cloud_instance_id
	if err := r.SetPathParam("cloud_instance_id", o.CloudInstanceID); err != nil {
		return err
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

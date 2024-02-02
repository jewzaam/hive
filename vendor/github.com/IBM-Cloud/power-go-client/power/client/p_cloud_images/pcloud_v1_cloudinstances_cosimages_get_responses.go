// Code generated by go-swagger; DO NOT EDIT.

package p_cloud_images

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"fmt"
	"io"

	"github.com/go-openapi/runtime"
	"github.com/go-openapi/strfmt"

	"github.com/IBM-Cloud/power-go-client/power/models"
)

// PcloudV1CloudinstancesCosimagesGetReader is a Reader for the PcloudV1CloudinstancesCosimagesGet structure.
type PcloudV1CloudinstancesCosimagesGetReader struct {
	formats strfmt.Registry
}

// ReadResponse reads a server response into the received o.
func (o *PcloudV1CloudinstancesCosimagesGetReader) ReadResponse(response runtime.ClientResponse, consumer runtime.Consumer) (interface{}, error) {
	switch response.Code() {
	case 200:
		result := NewPcloudV1CloudinstancesCosimagesGetOK()
		if err := result.readResponse(response, consumer, o.formats); err != nil {
			return nil, err
		}
		return result, nil
	case 400:
		result := NewPcloudV1CloudinstancesCosimagesGetBadRequest()
		if err := result.readResponse(response, consumer, o.formats); err != nil {
			return nil, err
		}
		return nil, result
	case 401:
		result := NewPcloudV1CloudinstancesCosimagesGetUnauthorized()
		if err := result.readResponse(response, consumer, o.formats); err != nil {
			return nil, err
		}
		return nil, result
	case 403:
		result := NewPcloudV1CloudinstancesCosimagesGetForbidden()
		if err := result.readResponse(response, consumer, o.formats); err != nil {
			return nil, err
		}
		return nil, result
	case 404:
		result := NewPcloudV1CloudinstancesCosimagesGetNotFound()
		if err := result.readResponse(response, consumer, o.formats); err != nil {
			return nil, err
		}
		return nil, result
	case 500:
		result := NewPcloudV1CloudinstancesCosimagesGetInternalServerError()
		if err := result.readResponse(response, consumer, o.formats); err != nil {
			return nil, err
		}
		return nil, result
	default:
		return nil, runtime.NewAPIError("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images] pcloud.v1.cloudinstances.cosimages.get", response, response.Code())
	}
}

// NewPcloudV1CloudinstancesCosimagesGetOK creates a PcloudV1CloudinstancesCosimagesGetOK with default headers values
func NewPcloudV1CloudinstancesCosimagesGetOK() *PcloudV1CloudinstancesCosimagesGetOK {
	return &PcloudV1CloudinstancesCosimagesGetOK{}
}

/*
PcloudV1CloudinstancesCosimagesGetOK describes a response with status code 200, with default header values.

OK
*/
type PcloudV1CloudinstancesCosimagesGetOK struct {
	Payload *models.Job
}

// IsSuccess returns true when this pcloud v1 cloudinstances cosimages get o k response has a 2xx status code
func (o *PcloudV1CloudinstancesCosimagesGetOK) IsSuccess() bool {
	return true
}

// IsRedirect returns true when this pcloud v1 cloudinstances cosimages get o k response has a 3xx status code
func (o *PcloudV1CloudinstancesCosimagesGetOK) IsRedirect() bool {
	return false
}

// IsClientError returns true when this pcloud v1 cloudinstances cosimages get o k response has a 4xx status code
func (o *PcloudV1CloudinstancesCosimagesGetOK) IsClientError() bool {
	return false
}

// IsServerError returns true when this pcloud v1 cloudinstances cosimages get o k response has a 5xx status code
func (o *PcloudV1CloudinstancesCosimagesGetOK) IsServerError() bool {
	return false
}

// IsCode returns true when this pcloud v1 cloudinstances cosimages get o k response a status code equal to that given
func (o *PcloudV1CloudinstancesCosimagesGetOK) IsCode(code int) bool {
	return code == 200
}

// Code gets the status code for the pcloud v1 cloudinstances cosimages get o k response
func (o *PcloudV1CloudinstancesCosimagesGetOK) Code() int {
	return 200
}

func (o *PcloudV1CloudinstancesCosimagesGetOK) Error() string {
	return fmt.Sprintf("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images][%d] pcloudV1CloudinstancesCosimagesGetOK  %+v", 200, o.Payload)
}

func (o *PcloudV1CloudinstancesCosimagesGetOK) String() string {
	return fmt.Sprintf("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images][%d] pcloudV1CloudinstancesCosimagesGetOK  %+v", 200, o.Payload)
}

func (o *PcloudV1CloudinstancesCosimagesGetOK) GetPayload() *models.Job {
	return o.Payload
}

func (o *PcloudV1CloudinstancesCosimagesGetOK) readResponse(response runtime.ClientResponse, consumer runtime.Consumer, formats strfmt.Registry) error {

	o.Payload = new(models.Job)

	// response payload
	if err := consumer.Consume(response.Body(), o.Payload); err != nil && err != io.EOF {
		return err
	}

	return nil
}

// NewPcloudV1CloudinstancesCosimagesGetBadRequest creates a PcloudV1CloudinstancesCosimagesGetBadRequest with default headers values
func NewPcloudV1CloudinstancesCosimagesGetBadRequest() *PcloudV1CloudinstancesCosimagesGetBadRequest {
	return &PcloudV1CloudinstancesCosimagesGetBadRequest{}
}

/*
PcloudV1CloudinstancesCosimagesGetBadRequest describes a response with status code 400, with default header values.

Bad Request
*/
type PcloudV1CloudinstancesCosimagesGetBadRequest struct {
	Payload *models.Error
}

// IsSuccess returns true when this pcloud v1 cloudinstances cosimages get bad request response has a 2xx status code
func (o *PcloudV1CloudinstancesCosimagesGetBadRequest) IsSuccess() bool {
	return false
}

// IsRedirect returns true when this pcloud v1 cloudinstances cosimages get bad request response has a 3xx status code
func (o *PcloudV1CloudinstancesCosimagesGetBadRequest) IsRedirect() bool {
	return false
}

// IsClientError returns true when this pcloud v1 cloudinstances cosimages get bad request response has a 4xx status code
func (o *PcloudV1CloudinstancesCosimagesGetBadRequest) IsClientError() bool {
	return true
}

// IsServerError returns true when this pcloud v1 cloudinstances cosimages get bad request response has a 5xx status code
func (o *PcloudV1CloudinstancesCosimagesGetBadRequest) IsServerError() bool {
	return false
}

// IsCode returns true when this pcloud v1 cloudinstances cosimages get bad request response a status code equal to that given
func (o *PcloudV1CloudinstancesCosimagesGetBadRequest) IsCode(code int) bool {
	return code == 400
}

// Code gets the status code for the pcloud v1 cloudinstances cosimages get bad request response
func (o *PcloudV1CloudinstancesCosimagesGetBadRequest) Code() int {
	return 400
}

func (o *PcloudV1CloudinstancesCosimagesGetBadRequest) Error() string {
	return fmt.Sprintf("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images][%d] pcloudV1CloudinstancesCosimagesGetBadRequest  %+v", 400, o.Payload)
}

func (o *PcloudV1CloudinstancesCosimagesGetBadRequest) String() string {
	return fmt.Sprintf("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images][%d] pcloudV1CloudinstancesCosimagesGetBadRequest  %+v", 400, o.Payload)
}

func (o *PcloudV1CloudinstancesCosimagesGetBadRequest) GetPayload() *models.Error {
	return o.Payload
}

func (o *PcloudV1CloudinstancesCosimagesGetBadRequest) readResponse(response runtime.ClientResponse, consumer runtime.Consumer, formats strfmt.Registry) error {

	o.Payload = new(models.Error)

	// response payload
	if err := consumer.Consume(response.Body(), o.Payload); err != nil && err != io.EOF {
		return err
	}

	return nil
}

// NewPcloudV1CloudinstancesCosimagesGetUnauthorized creates a PcloudV1CloudinstancesCosimagesGetUnauthorized with default headers values
func NewPcloudV1CloudinstancesCosimagesGetUnauthorized() *PcloudV1CloudinstancesCosimagesGetUnauthorized {
	return &PcloudV1CloudinstancesCosimagesGetUnauthorized{}
}

/*
PcloudV1CloudinstancesCosimagesGetUnauthorized describes a response with status code 401, with default header values.

Unauthorized
*/
type PcloudV1CloudinstancesCosimagesGetUnauthorized struct {
	Payload *models.Error
}

// IsSuccess returns true when this pcloud v1 cloudinstances cosimages get unauthorized response has a 2xx status code
func (o *PcloudV1CloudinstancesCosimagesGetUnauthorized) IsSuccess() bool {
	return false
}

// IsRedirect returns true when this pcloud v1 cloudinstances cosimages get unauthorized response has a 3xx status code
func (o *PcloudV1CloudinstancesCosimagesGetUnauthorized) IsRedirect() bool {
	return false
}

// IsClientError returns true when this pcloud v1 cloudinstances cosimages get unauthorized response has a 4xx status code
func (o *PcloudV1CloudinstancesCosimagesGetUnauthorized) IsClientError() bool {
	return true
}

// IsServerError returns true when this pcloud v1 cloudinstances cosimages get unauthorized response has a 5xx status code
func (o *PcloudV1CloudinstancesCosimagesGetUnauthorized) IsServerError() bool {
	return false
}

// IsCode returns true when this pcloud v1 cloudinstances cosimages get unauthorized response a status code equal to that given
func (o *PcloudV1CloudinstancesCosimagesGetUnauthorized) IsCode(code int) bool {
	return code == 401
}

// Code gets the status code for the pcloud v1 cloudinstances cosimages get unauthorized response
func (o *PcloudV1CloudinstancesCosimagesGetUnauthorized) Code() int {
	return 401
}

func (o *PcloudV1CloudinstancesCosimagesGetUnauthorized) Error() string {
	return fmt.Sprintf("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images][%d] pcloudV1CloudinstancesCosimagesGetUnauthorized  %+v", 401, o.Payload)
}

func (o *PcloudV1CloudinstancesCosimagesGetUnauthorized) String() string {
	return fmt.Sprintf("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images][%d] pcloudV1CloudinstancesCosimagesGetUnauthorized  %+v", 401, o.Payload)
}

func (o *PcloudV1CloudinstancesCosimagesGetUnauthorized) GetPayload() *models.Error {
	return o.Payload
}

func (o *PcloudV1CloudinstancesCosimagesGetUnauthorized) readResponse(response runtime.ClientResponse, consumer runtime.Consumer, formats strfmt.Registry) error {

	o.Payload = new(models.Error)

	// response payload
	if err := consumer.Consume(response.Body(), o.Payload); err != nil && err != io.EOF {
		return err
	}

	return nil
}

// NewPcloudV1CloudinstancesCosimagesGetForbidden creates a PcloudV1CloudinstancesCosimagesGetForbidden with default headers values
func NewPcloudV1CloudinstancesCosimagesGetForbidden() *PcloudV1CloudinstancesCosimagesGetForbidden {
	return &PcloudV1CloudinstancesCosimagesGetForbidden{}
}

/*
PcloudV1CloudinstancesCosimagesGetForbidden describes a response with status code 403, with default header values.

Forbidden
*/
type PcloudV1CloudinstancesCosimagesGetForbidden struct {
	Payload *models.Error
}

// IsSuccess returns true when this pcloud v1 cloudinstances cosimages get forbidden response has a 2xx status code
func (o *PcloudV1CloudinstancesCosimagesGetForbidden) IsSuccess() bool {
	return false
}

// IsRedirect returns true when this pcloud v1 cloudinstances cosimages get forbidden response has a 3xx status code
func (o *PcloudV1CloudinstancesCosimagesGetForbidden) IsRedirect() bool {
	return false
}

// IsClientError returns true when this pcloud v1 cloudinstances cosimages get forbidden response has a 4xx status code
func (o *PcloudV1CloudinstancesCosimagesGetForbidden) IsClientError() bool {
	return true
}

// IsServerError returns true when this pcloud v1 cloudinstances cosimages get forbidden response has a 5xx status code
func (o *PcloudV1CloudinstancesCosimagesGetForbidden) IsServerError() bool {
	return false
}

// IsCode returns true when this pcloud v1 cloudinstances cosimages get forbidden response a status code equal to that given
func (o *PcloudV1CloudinstancesCosimagesGetForbidden) IsCode(code int) bool {
	return code == 403
}

// Code gets the status code for the pcloud v1 cloudinstances cosimages get forbidden response
func (o *PcloudV1CloudinstancesCosimagesGetForbidden) Code() int {
	return 403
}

func (o *PcloudV1CloudinstancesCosimagesGetForbidden) Error() string {
	return fmt.Sprintf("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images][%d] pcloudV1CloudinstancesCosimagesGetForbidden  %+v", 403, o.Payload)
}

func (o *PcloudV1CloudinstancesCosimagesGetForbidden) String() string {
	return fmt.Sprintf("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images][%d] pcloudV1CloudinstancesCosimagesGetForbidden  %+v", 403, o.Payload)
}

func (o *PcloudV1CloudinstancesCosimagesGetForbidden) GetPayload() *models.Error {
	return o.Payload
}

func (o *PcloudV1CloudinstancesCosimagesGetForbidden) readResponse(response runtime.ClientResponse, consumer runtime.Consumer, formats strfmt.Registry) error {

	o.Payload = new(models.Error)

	// response payload
	if err := consumer.Consume(response.Body(), o.Payload); err != nil && err != io.EOF {
		return err
	}

	return nil
}

// NewPcloudV1CloudinstancesCosimagesGetNotFound creates a PcloudV1CloudinstancesCosimagesGetNotFound with default headers values
func NewPcloudV1CloudinstancesCosimagesGetNotFound() *PcloudV1CloudinstancesCosimagesGetNotFound {
	return &PcloudV1CloudinstancesCosimagesGetNotFound{}
}

/*
PcloudV1CloudinstancesCosimagesGetNotFound describes a response with status code 404, with default header values.

Not Found
*/
type PcloudV1CloudinstancesCosimagesGetNotFound struct {
	Payload *models.Error
}

// IsSuccess returns true when this pcloud v1 cloudinstances cosimages get not found response has a 2xx status code
func (o *PcloudV1CloudinstancesCosimagesGetNotFound) IsSuccess() bool {
	return false
}

// IsRedirect returns true when this pcloud v1 cloudinstances cosimages get not found response has a 3xx status code
func (o *PcloudV1CloudinstancesCosimagesGetNotFound) IsRedirect() bool {
	return false
}

// IsClientError returns true when this pcloud v1 cloudinstances cosimages get not found response has a 4xx status code
func (o *PcloudV1CloudinstancesCosimagesGetNotFound) IsClientError() bool {
	return true
}

// IsServerError returns true when this pcloud v1 cloudinstances cosimages get not found response has a 5xx status code
func (o *PcloudV1CloudinstancesCosimagesGetNotFound) IsServerError() bool {
	return false
}

// IsCode returns true when this pcloud v1 cloudinstances cosimages get not found response a status code equal to that given
func (o *PcloudV1CloudinstancesCosimagesGetNotFound) IsCode(code int) bool {
	return code == 404
}

// Code gets the status code for the pcloud v1 cloudinstances cosimages get not found response
func (o *PcloudV1CloudinstancesCosimagesGetNotFound) Code() int {
	return 404
}

func (o *PcloudV1CloudinstancesCosimagesGetNotFound) Error() string {
	return fmt.Sprintf("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images][%d] pcloudV1CloudinstancesCosimagesGetNotFound  %+v", 404, o.Payload)
}

func (o *PcloudV1CloudinstancesCosimagesGetNotFound) String() string {
	return fmt.Sprintf("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images][%d] pcloudV1CloudinstancesCosimagesGetNotFound  %+v", 404, o.Payload)
}

func (o *PcloudV1CloudinstancesCosimagesGetNotFound) GetPayload() *models.Error {
	return o.Payload
}

func (o *PcloudV1CloudinstancesCosimagesGetNotFound) readResponse(response runtime.ClientResponse, consumer runtime.Consumer, formats strfmt.Registry) error {

	o.Payload = new(models.Error)

	// response payload
	if err := consumer.Consume(response.Body(), o.Payload); err != nil && err != io.EOF {
		return err
	}

	return nil
}

// NewPcloudV1CloudinstancesCosimagesGetInternalServerError creates a PcloudV1CloudinstancesCosimagesGetInternalServerError with default headers values
func NewPcloudV1CloudinstancesCosimagesGetInternalServerError() *PcloudV1CloudinstancesCosimagesGetInternalServerError {
	return &PcloudV1CloudinstancesCosimagesGetInternalServerError{}
}

/*
PcloudV1CloudinstancesCosimagesGetInternalServerError describes a response with status code 500, with default header values.

Internal Server Error
*/
type PcloudV1CloudinstancesCosimagesGetInternalServerError struct {
	Payload *models.Error
}

// IsSuccess returns true when this pcloud v1 cloudinstances cosimages get internal server error response has a 2xx status code
func (o *PcloudV1CloudinstancesCosimagesGetInternalServerError) IsSuccess() bool {
	return false
}

// IsRedirect returns true when this pcloud v1 cloudinstances cosimages get internal server error response has a 3xx status code
func (o *PcloudV1CloudinstancesCosimagesGetInternalServerError) IsRedirect() bool {
	return false
}

// IsClientError returns true when this pcloud v1 cloudinstances cosimages get internal server error response has a 4xx status code
func (o *PcloudV1CloudinstancesCosimagesGetInternalServerError) IsClientError() bool {
	return false
}

// IsServerError returns true when this pcloud v1 cloudinstances cosimages get internal server error response has a 5xx status code
func (o *PcloudV1CloudinstancesCosimagesGetInternalServerError) IsServerError() bool {
	return true
}

// IsCode returns true when this pcloud v1 cloudinstances cosimages get internal server error response a status code equal to that given
func (o *PcloudV1CloudinstancesCosimagesGetInternalServerError) IsCode(code int) bool {
	return code == 500
}

// Code gets the status code for the pcloud v1 cloudinstances cosimages get internal server error response
func (o *PcloudV1CloudinstancesCosimagesGetInternalServerError) Code() int {
	return 500
}

func (o *PcloudV1CloudinstancesCosimagesGetInternalServerError) Error() string {
	return fmt.Sprintf("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images][%d] pcloudV1CloudinstancesCosimagesGetInternalServerError  %+v", 500, o.Payload)
}

func (o *PcloudV1CloudinstancesCosimagesGetInternalServerError) String() string {
	return fmt.Sprintf("[GET /pcloud/v1/cloud-instances/{cloud_instance_id}/cos-images][%d] pcloudV1CloudinstancesCosimagesGetInternalServerError  %+v", 500, o.Payload)
}

func (o *PcloudV1CloudinstancesCosimagesGetInternalServerError) GetPayload() *models.Error {
	return o.Payload
}

func (o *PcloudV1CloudinstancesCosimagesGetInternalServerError) readResponse(response runtime.ClientResponse, consumer runtime.Consumer, formats strfmt.Registry) error {

	o.Payload = new(models.Error)

	// response payload
	if err := consumer.Consume(response.Body(), o.Payload); err != nil && err != io.EOF {
		return err
	}

	return nil
}

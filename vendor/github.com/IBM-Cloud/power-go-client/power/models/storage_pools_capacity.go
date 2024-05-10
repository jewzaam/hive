// Code generated by go-swagger; DO NOT EDIT.

package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"context"
	"strconv"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
)

// StoragePoolsCapacity Storage capacity for all storage pools
//
// swagger:model StoragePoolsCapacity
type StoragePoolsCapacity struct {

	// maximum storage allocation
	MaximumStorageAllocation *MaximumStorageAllocation `json:"maximumStorageAllocation,omitempty"`

	// storage pools capacity
	StoragePoolsCapacity []*StoragePoolCapacity `json:"storagePoolsCapacity"`
}

// Validate validates this storage pools capacity
func (m *StoragePoolsCapacity) Validate(formats strfmt.Registry) error {
	var res []error

	if err := m.validateMaximumStorageAllocation(formats); err != nil {
		res = append(res, err)
	}

	if err := m.validateStoragePoolsCapacity(formats); err != nil {
		res = append(res, err)
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

func (m *StoragePoolsCapacity) validateMaximumStorageAllocation(formats strfmt.Registry) error {
	if swag.IsZero(m.MaximumStorageAllocation) { // not required
		return nil
	}

	if m.MaximumStorageAllocation != nil {
		if err := m.MaximumStorageAllocation.Validate(formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("maximumStorageAllocation")
			} else if ce, ok := err.(*errors.CompositeError); ok {
				return ce.ValidateName("maximumStorageAllocation")
			}
			return err
		}
	}

	return nil
}

func (m *StoragePoolsCapacity) validateStoragePoolsCapacity(formats strfmt.Registry) error {
	if swag.IsZero(m.StoragePoolsCapacity) { // not required
		return nil
	}

	for i := 0; i < len(m.StoragePoolsCapacity); i++ {
		if swag.IsZero(m.StoragePoolsCapacity[i]) { // not required
			continue
		}

		if m.StoragePoolsCapacity[i] != nil {
			if err := m.StoragePoolsCapacity[i].Validate(formats); err != nil {
				if ve, ok := err.(*errors.Validation); ok {
					return ve.ValidateName("storagePoolsCapacity" + "." + strconv.Itoa(i))
				} else if ce, ok := err.(*errors.CompositeError); ok {
					return ce.ValidateName("storagePoolsCapacity" + "." + strconv.Itoa(i))
				}
				return err
			}
		}

	}

	return nil
}

// ContextValidate validate this storage pools capacity based on the context it is used
func (m *StoragePoolsCapacity) ContextValidate(ctx context.Context, formats strfmt.Registry) error {
	var res []error

	if err := m.contextValidateMaximumStorageAllocation(ctx, formats); err != nil {
		res = append(res, err)
	}

	if err := m.contextValidateStoragePoolsCapacity(ctx, formats); err != nil {
		res = append(res, err)
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

func (m *StoragePoolsCapacity) contextValidateMaximumStorageAllocation(ctx context.Context, formats strfmt.Registry) error {

	if m.MaximumStorageAllocation != nil {

		if swag.IsZero(m.MaximumStorageAllocation) { // not required
			return nil
		}

		if err := m.MaximumStorageAllocation.ContextValidate(ctx, formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("maximumStorageAllocation")
			} else if ce, ok := err.(*errors.CompositeError); ok {
				return ce.ValidateName("maximumStorageAllocation")
			}
			return err
		}
	}

	return nil
}

func (m *StoragePoolsCapacity) contextValidateStoragePoolsCapacity(ctx context.Context, formats strfmt.Registry) error {

	for i := 0; i < len(m.StoragePoolsCapacity); i++ {

		if m.StoragePoolsCapacity[i] != nil {

			if swag.IsZero(m.StoragePoolsCapacity[i]) { // not required
				return nil
			}

			if err := m.StoragePoolsCapacity[i].ContextValidate(ctx, formats); err != nil {
				if ve, ok := err.(*errors.Validation); ok {
					return ve.ValidateName("storagePoolsCapacity" + "." + strconv.Itoa(i))
				} else if ce, ok := err.(*errors.CompositeError); ok {
					return ce.ValidateName("storagePoolsCapacity" + "." + strconv.Itoa(i))
				}
				return err
			}
		}

	}

	return nil
}

// MarshalBinary interface implementation
func (m *StoragePoolsCapacity) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return swag.WriteJSON(m)
}

// UnmarshalBinary interface implementation
func (m *StoragePoolsCapacity) UnmarshalBinary(b []byte) error {
	var res StoragePoolsCapacity
	if err := swag.ReadJSON(b, &res); err != nil {
		return err
	}
	*m = res
	return nil
}
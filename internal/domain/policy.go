package domain

type EndPolicy string

const (
	EndPolicyEditInPlace EndPolicy = "edit_in_place"
	EndPolicyNewMessage  EndPolicy = "new_message"
	EndPolicyDelete      EndPolicy = "delete"
	EndPolicyNone        EndPolicy = "none"
)

func (p EndPolicy) Valid() bool {
	switch p {
	case EndPolicyEditInPlace, EndPolicyNewMessage, EndPolicyDelete, EndPolicyNone:
		return true
	}
	return false
}

package domain

type EndPolicy string

const (
	EndPolicyEditInPlace EndPolicy = "edit_in_place"
	EndPolicyNewMessage  EndPolicy = "new_message"
	EndPolicyReplace     EndPolicy = "replace"
	EndPolicyDelete      EndPolicy = "delete"
	EndPolicyNone        EndPolicy = "none"
)

func (p EndPolicy) Valid() bool {
	switch p {
	case EndPolicyEditInPlace, EndPolicyNewMessage, EndPolicyReplace, EndPolicyDelete, EndPolicyNone:
		return true
	}
	return false
}

// NeedsRecording returns true if the end action renders the offline message
func (p EndPolicy) NeedsRecording() bool {
	switch p {
	case EndPolicyEditInPlace, EndPolicyNewMessage, EndPolicyReplace:
		return true
	}
	return false
}

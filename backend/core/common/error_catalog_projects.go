package common

func init() {
	registerAdditionalError("conversation project invalid_input", 400, 2002810)
	registerAdditionalError("conversation project invalid_name", 400, 2002811)
	registerAdditionalError("conversation project membership_locked", 409, 2002812)
	registerAdditionalError("conversation project directory_conflict", 409, 2002813)
}

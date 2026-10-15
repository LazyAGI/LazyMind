package common

func init() {
	registerAdditionalErrorAlias("directory model failed", "document directory operation failed", 500, 2003206)
	registerAdditionalErrorAlias("document directory execution lease lost", "document directory is organizing", 409, 2003201)
	registerAdditionalError("document directory is organizing", 409, 2003201)
	registerAdditionalError("document directory capacity is busy", 409, 2003202)
	registerAdditionalError("document directory pin limit reached", 409, 2003203)
	registerAdditionalError("invalid document directory input", 400, 2003204)
	registerAdditionalError("document directory not found", 404, 2003205)
	registerAdditionalError("document directory operation failed", 500, 2003206)
	registerAdditionalError("mail_unavailable", 409, 2003210)
	registerAdditionalError("mail_authorization_failed", 409, 2003211)
	registerAdditionalError("mail_locator_missing", 404, 2003212)
	registerAdditionalError("mail_location_changed", 404, 2003213)
	registerAdditionalError("mail_timeout", 504, 2003214)
	registerAdditionalError("mail_read_failed", 502, 2003215)
}

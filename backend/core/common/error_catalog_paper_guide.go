package common

func init() {
	registerAdditionalError("paper_guide_regeneration_required", 409, 2004413)
	registerAdditionalErrorAlias("整篇导读仅支持按需或禁用", "paper_guide_disabled", 403, 2004410)
	registerAdditionalError("paper_guide_source_changed", 409, 2004401)
	registerAdditionalError("paper_guide_preparation_required", 409, 2004402)
	registerAdditionalError("paper_guide_model_unavailable", 422, 2004403)
	registerAdditionalError("paper_guide_budget_exhausted", 422, 2004404)
	registerAdditionalError("paper_guide_context_too_long", 422, 2004405)
	registerAdditionalError("paper_guide_invalid_evidence", 422, 2004406)
	registerAdditionalError("paper_guide_material_insufficient", 422, 2004407)
	registerAdditionalError("paper_guide_permission_revoked", 404, 2004408)
	registerAdditionalError("paper_guide_conflict", 409, 2004409)
	registerAdditionalError("paper_guide_disabled", 403, 2004410)
	registerAdditionalError("paper_guide_review_required", 422, 2004411)
	registerAdditionalError("paper_guide_generation_failed", 500, 2004412)
}

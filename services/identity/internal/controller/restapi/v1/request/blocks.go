package request

// ListBlocks is the query of GET /me/blocks.
type ListBlocks struct {
	// Fields "ids" returns only the blocked user ids (at most 1000, one page).
	Fields string `query:"fields"`
	Cursor string `query:"cursor"`
	// Limit is 1-50 (default 20).
	Limit int `query:"limit"`
}

const _maxBlockPage = 50

// Validate returns the field errors (NXL-00 §4.1).
func (q ListBlocks) Validate() map[string]string {
	errs := map[string]string{}

	if q.Fields != "" && q.Fields != "ids" {
		errs["fields"] = "ENUM"
	}

	if q.Limit < 0 || q.Limit > _maxBlockPage {
		errs["limit"] = "MAX"
	}

	return errs
}

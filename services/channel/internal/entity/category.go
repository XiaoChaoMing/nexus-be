package entity

// CategoryGroup -.
type CategoryGroup string

// Category groups (categories.group_code).
const (
	GroupGames    CategoryGroup = "GAMES"
	GroupEsports  CategoryGroup = "ESPORTS"
	GroupMusic    CategoryGroup = "MUSIC"
	GroupIRL      CategoryGroup = "IRL"
	GroupCreative CategoryGroup = "CREATIVE"
)

// Valid -.
func (g CategoryGroup) Valid() bool {
	switch g {
	case GroupGames, GroupEsports, GroupMusic, GroupIRL, GroupCreative:
		return true
	}

	return false
}

// CategorySort -.
type CategorySort string

// Category orders: seed position, or live viewers (ties keep the seed position, G4-Q4).
const (
	CategoryByPosition CategorySort = "position"
	CategoryByViewers  CategorySort = "viewers"
)

// Valid -.
func (s CategorySort) Valid() bool {
	return s == CategoryByPosition || s == CategoryByViewers
}

// Category -.
type Category struct {
	ID        string
	Slug      string
	Name      string
	Group     CategoryGroup
	PosterURL *string
	Tags      []string
	Position  int
	// ViewerCount is the sum over the category's live streams; nil when counts are unavailable.
	ViewerCount *int
}

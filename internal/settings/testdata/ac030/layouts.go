package sample

// Negative fixture: home.max_saved_layouts is 10.
const maxSavedLayouts = 10

func local() int {
	saved := 10
	var also = 10
	return saved + also
}

package activity

// ValidInstanceCoverImageURL accepts an empty cover, the public relative cover
// path the runtime serves (CoverImagePath in the api package:
// /api/v1/xiangwan/covers/<32hex>.<ext>, the shape the upload endpoint
// returns), or an absolute https URL without a fragment. It deliberately
// mirrors the migration 787 cover_image_url CHECK constraint; the 783 brand
// hero URL contract stays https-only. Dev runtimes behind an http origin
// store the relative form as-is, and public reads emit the stored value
// unchanged.
func ValidInstanceCoverImageURL(value string) bool {
	return value == "" || detailBlockCoverPathPattern.MatchString(value) ||
		validHTTPSHeroURL(value)
}

ALTER TABLE xiangwan_brand_profile_publications
    DROP CONSTRAINT IF EXISTS xw_brand_publication_hero_image_check,
    DROP CONSTRAINT IF EXISTS xw_brand_publication_hero_text_check,
    DROP CONSTRAINT IF EXISTS xw_brand_publication_hero_mode_check,
    DROP COLUMN IF EXISTS hero_image_alt,
    DROP COLUMN IF EXISTS hero_image_url,
    DROP COLUMN IF EXISTS hero_subtitle,
    DROP COLUMN IF EXISTS hero_eyebrow,
    DROP COLUMN IF EXISTS hero_mode;

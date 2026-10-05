-- 783_xiangwan_brand_hero_publication.up.sql
-- Extend the immutable BrandProfile publication with the governed homepage
-- hero presentation. Existing publications remain valid text-mode snapshots.

ALTER TABLE xiangwan_brand_profile_publications
    ADD COLUMN hero_mode VARCHAR(16) NOT NULL DEFAULT 'text',
    ADD COLUMN hero_eyebrow VARCHAR(100) NOT NULL DEFAULT 'TIANJIN AI COMMUNITY',
    ADD COLUMN hero_subtitle VARCHAR(200) NOT NULL DEFAULT '',
    ADD COLUMN hero_image_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN hero_image_alt VARCHAR(200) NOT NULL DEFAULT '',
    ADD CONSTRAINT xw_brand_publication_hero_mode_check
        CHECK (hero_mode IN ('text', 'image')),
    ADD CONSTRAINT xw_brand_publication_hero_text_check
        CHECK (
            hero_eyebrow = BTRIM(hero_eyebrow)
            AND CHAR_LENGTH(hero_eyebrow) BETWEEN 1 AND 100
            AND hero_subtitle = BTRIM(hero_subtitle)
            AND CHAR_LENGTH(hero_subtitle) BETWEEN 0 AND 200
            AND hero_eyebrow !~ '[[:cntrl:]]'
            AND hero_subtitle !~ '[[:cntrl:]]'
        ),
    ADD CONSTRAINT xw_brand_publication_hero_image_check
        CHECK (
            (
                hero_mode = 'text'
                AND hero_image_url = ''
                AND hero_image_alt = ''
            ) OR (
                hero_mode = 'image'
                AND CHAR_LENGTH(hero_image_url) BETWEEN 9 AND 2048
                AND hero_image_url ~ '^https://[^[:space:]]+$'
                AND POSITION('#' IN hero_image_url) = 0
                AND hero_image_alt = BTRIM(hero_image_alt)
                AND CHAR_LENGTH(hero_image_alt) BETWEEN 1 AND 200
                AND hero_image_alt !~ '[[:cntrl:]]'
            )
        );

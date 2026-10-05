-- Home content is optional in both presentation modes. Existing immutable
-- publications are unchanged; a new publication may omit the banner or alt.
ALTER TABLE xiangwan_brand_profile_publications
    DROP CONSTRAINT IF EXISTS xw_brand_publication_hero_image_check,
    ADD CONSTRAINT xw_brand_publication_hero_image_check CHECK (
        (hero_mode = 'text' AND hero_image_url = '' AND hero_image_alt = '')
        OR (
            hero_mode = 'image'
            AND (
                hero_image_url = '' OR (
                    CHAR_LENGTH(hero_image_url) BETWEEN 9 AND 2048
                    AND hero_image_url ~ '^https://[^[:space:]]+$'
                    AND POSITION('#' IN hero_image_url) = 0
                )
            )
            AND hero_image_alt = BTRIM(hero_image_alt)
            AND CHAR_LENGTH(hero_image_alt) BETWEEN 0 AND 200
            AND hero_image_alt !~ '[[:cntrl:]]'
        )
    );

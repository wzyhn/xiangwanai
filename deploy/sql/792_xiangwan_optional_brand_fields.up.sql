-- 792_xiangwan_optional_brand_fields.up.sql
-- Homepage brand copy is optional. An empty value means that the matching
-- visual row is omitted by the Mini Program; immutable publications still
-- retain the exact operator-authored snapshot.

ALTER TABLE xiangwan_brand_profile_publications
    DROP CONSTRAINT IF EXISTS xw_brand_publication_name_check,
    ADD CONSTRAINT xw_brand_publication_name_check
        CHECK (
            community_name = BTRIM(community_name)
            AND CHAR_LENGTH(community_name) BETWEEN 0 AND 100
        ),
    DROP CONSTRAINT IF EXISTS xw_brand_publication_intro_check,
    ADD CONSTRAINT xw_brand_publication_intro_check
        CHECK (
            brand_intro = BTRIM(brand_intro)
            AND CHAR_LENGTH(brand_intro) BETWEEN 0 AND 2000
        ),
    DROP CONSTRAINT IF EXISTS xw_brand_publication_hero_text_check,
    ADD CONSTRAINT xw_brand_publication_hero_text_check
        CHECK (
            hero_eyebrow = BTRIM(hero_eyebrow)
            AND CHAR_LENGTH(hero_eyebrow) BETWEEN 0 AND 100
            AND hero_subtitle = BTRIM(hero_subtitle)
            AND CHAR_LENGTH(hero_subtitle) BETWEEN 0 AND 200
            AND hero_eyebrow !~ '[[:cntrl:]]'
            AND hero_subtitle !~ '[[:cntrl:]]'
        );

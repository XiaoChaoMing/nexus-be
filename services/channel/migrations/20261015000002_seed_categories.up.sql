-- Categories of NXL-C §6 (position = seed order, G4-Q4); covers are generated at start-up (EnsureCategoryCovers).
INSERT INTO categories (slug, name, group_code, position, tags, poster_url) VALUES
    ('lien-minh-huyen-thoai', 'Liên Minh Huyền Thoại', 'GAMES',     1, '{MOBA,PC}',               '/media/categories/lien-minh-huyen-thoai.jpg'),
    ('valorant',              'Valorant',              'GAMES',     2, '{FPS,"Chiến thuật"}',     '/media/categories/valorant.jpg'),
    ('lien-quan-mobile',      'Liên Quân Mobile',      'GAMES',     3, '{MOBA,Mobile}',           '/media/categories/lien-quan-mobile.jpg'),
    ('counter-strike-2',      'Counter-Strike 2',      'GAMES',     4, '{FPS,PC}',                '/media/categories/counter-strike-2.jpg'),
    ('fc-online',             'FC Online',             'GAMES',     5, '{"Bóng đá","Thể thao"}',  '/media/categories/fc-online.jpg'),
    ('minecraft',             'Minecraft',             'GAMES',     6, '{Sandbox,"Sinh tồn"}',    '/media/categories/minecraft.jpg'),
    ('dota-2',                'Dota 2',                'GAMES',     7, '{MOBA,PC}',               '/media/categories/dota-2.jpg'),
    ('grand-theft-auto-v',    'Grand Theft Auto V',    'GAMES',     8, '{"Thế giới mở",RP}',      '/media/categories/grand-theft-auto-v.jpg'),
    ('pubg-battlegrounds',    'PUBG: Battlegrounds',   'GAMES',     9, '{"Battle Royale",FPS}',   '/media/categories/pubg-battlegrounds.jpg'),
    ('esports-lmht',          'Esports Liên Minh',     'ESPORTS',  10, '{"Giải đấu",MOBA}',         '/media/categories/esports-lmht.jpg'),
    ('esports-valorant',      'Esports Valorant',      'ESPORTS',  11, '{"Giải đấu",FPS}',          '/media/categories/esports-valorant.jpg'),
    ('esports-mobile',        'Esports Mobile',        'ESPORTS',  12, '{"Giải đấu",Mobile}',       '/media/categories/esports-mobile.jpg'),
    ('esports-cs',            'Esports Counter-Strike','ESPORTS',  13, '{"Giải đấu",FPS}',          '/media/categories/esports-cs.jpg'),
    ('am-nhac',               'Âm nhạc',               'MUSIC',    14, '{Live,"Biểu diễn"}',      '/media/categories/am-nhac.jpg'),
    ('acoustic',              'Acoustic & Hát live',   'MUSIC',    15, '{Acoustic,"Hát"}',        '/media/categories/acoustic.jpg'),
    ('dj-edm',                'DJ & EDM',              'MUSIC',    16, '{DJ,EDM}',                '/media/categories/dj-edm.jpg'),
    ('san-xuat-am-nhac',      'Sản xuất âm nhạc',      'MUSIC',    17, '{Producer,Beat}',         '/media/categories/san-xuat-am-nhac.jpg'),
    ('tro-chuyen',            'Trò chuyện',            'IRL',      18, '{"Tâm sự",IRL}',          '/media/categories/tro-chuyen.jpg'),
    ('du-lich',               'Du lịch & Ngoài trời',  'IRL',      19, '{"Du lịch",IRL}',         '/media/categories/du-lich.jpg'),
    ('am-thuc',               'Ẩm thực',               'IRL',      20, '{"Nấu ăn",Mukbang}',      '/media/categories/am-thuc.jpg'),
    ('the-thao',              'Thể thao & Fitness',    'IRL',      21, '{Gym,"Thể thao"}',        '/media/categories/the-thao.jpg'),
    ('ve-minh-hoa',           'Vẽ & Minh họa',         'CREATIVE', 22, '{"Vẽ",Art}',              '/media/categories/ve-minh-hoa.jpg'),
    ('lap-trinh',             'Lập trình & Công nghệ', 'CREATIVE', 23, '{Code,Tech}',             '/media/categories/lap-trinh.jpg'),
    ('thu-cong',              'Thủ công & DIY',        'CREATIVE', 24, '{DIY,Handmade}',          '/media/categories/thu-cong.jpg')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO search_documents (entity_type, entity_id, title, keywords, category_id, image_url, target)
SELECT 'CATEGORY', id, name, slug || ' ' || array_to_string(tags, ' '), id, poster_url, '/browse/' || slug
FROM categories
ON CONFLICT (entity_type, entity_id) DO NOTHING;

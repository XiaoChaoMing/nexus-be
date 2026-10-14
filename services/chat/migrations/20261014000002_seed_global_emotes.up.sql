-- Global emotes from the NexusLive design (deploy/emotes/*.webp, served by the edge at /emotes/).
INSERT INTO emotes (scope, code, image_url)
VALUES
    ('GLOBAL', 'nxGG', '/emotes/nxGG.webp'),
    ('GLOBAL', 'nxHype', '/emotes/nxHype.webp'),
    ('GLOBAL', 'nxHaha', '/emotes/nxHaha.webp'),
    ('GLOBAL', 'nxTim', '/emotes/nxTim.webp'),
    ('GLOBAL', 'nxKhoc', '/emotes/nxKhoc.webp'),
    ('GLOBAL', 'nxUa', '/emotes/nxUa.webp'),
    ('GLOBAL', 'nxNgau', '/emotes/nxNgau.webp'),
    ('GLOBAL', 'nxCup', '/emotes/nxCup.webp'),
    ('GLOBAL', 'nxCay', '/emotes/nxCay.webp'),
    ('GLOBAL', 'nxGhe', '/emotes/nxGhe.webp'),
    ('GLOBAL', 'nxChao', '/emotes/nxChao.webp'),
    ('GLOBAL', 'nxPog', '/emotes/nxPog.webp')
ON CONFLICT DO NOTHING;

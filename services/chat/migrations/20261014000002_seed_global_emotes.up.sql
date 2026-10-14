-- Global emotes drawn for NexusLive (deploy/emotes/*.svg, served by the edge at /emotes/).
INSERT INTO emotes (scope, code, image_url)
VALUES
    ('GLOBAL', 'nxlGG', '/emotes/nxlGG.svg'),
    ('GLOBAL', 'nxlHype', '/emotes/nxlHype.svg'),
    ('GLOBAL', 'nxlLol', '/emotes/nxlLol.svg'),
    ('GLOBAL', 'nxlLove', '/emotes/nxlLove.svg'),
    ('GLOBAL', 'nxlSad', '/emotes/nxlSad.svg'),
    ('GLOBAL', 'nxlWow', '/emotes/nxlWow.svg'),
    ('GLOBAL', 'nxlCool', '/emotes/nxlCool.svg'),
    ('GLOBAL', 'nxlClap', '/emotes/nxlClap.svg'),
    ('GLOBAL', 'nxlFire', '/emotes/nxlFire.svg'),
    ('GLOBAL', 'nxlThink', '/emotes/nxlThink.svg'),
    ('GLOBAL', 'nxlWave', '/emotes/nxlWave.svg'),
    ('GLOBAL', 'nxlPog', '/emotes/nxlPog.svg')
ON CONFLICT DO NOTHING;

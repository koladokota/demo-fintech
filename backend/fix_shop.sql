
SET client_encoding = 'UTF8';

UPDATE shop_items
SET name = 'Неоновый след',
    description = 'Светящийся след за ракетой'
WHERE id = 'neon_trail';

UPDATE shop_items
SET name = 'Космическая ракета',
    description = 'Уникальный внешний вид ракеты'
WHERE id = 'rocket_skin';

UPDATE shop_items
SET name = 'Эффект победы',
    description = 'Анимация после победы'
WHERE id = 'victory_effect';

UPDATE shop_items
SET name = 'Золотой профиль',
    description = 'Золотое оформление профиля'
WHERE id = 'gold_profile';

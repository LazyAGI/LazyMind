-- Clean up pending user_preference review records generated before
-- YAML frontmatter keys were unified to Chinese (智能体角色/用户称谓/回复风格).
DELETE FROM public.memory_review
WHERE target = 'user_preference'
  AND review_status = 'pending';

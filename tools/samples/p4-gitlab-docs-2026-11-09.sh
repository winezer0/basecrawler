https://docs.gitlab.com/review-mr
https://docs.gitlab.com/upstream-review-mr
https://docs.gitlab.com/1
https://docs.gitlab.com/2
https://docs.gitlab.com/3
https://docs.gitlab.com/api/rest/
https://gitlab.example.com/api/v4/projects
https://docs.gitlab.com/apple-touch-icon.png
https://docs.gitlab.com/favicon.ico
https://docs.gitlab.com/favicon.svg
https://docs.gitlab.com/manifests/manifest.webmanifest
https://docs.gitlab.com/gitlab_ui/fonts/GitLabSans.woff2
https://docs.gitlab.com/gitlab_ui/fonts/GitLabSans-Italic.woff2
https://docs.gitlab.com/gitlab_ui/fonts/GitLabMono.woff2
https://docs.gitlab.com/gitlab_ui/ui/index.css
https://docs.gitlab.com/vite/main.css
https://docs.gitlab.com/css/syntax-dark.min.css
https://docs.gitlab.com/css/syntax-light.min.css
https://docs.gitlab.com/vite/elastic_search.js
https://docs.gitlab.com/vite/history.js
https://docs.gitlab.com/vite/page_content.css
https://docs.gitlab.com/gitlab_ui/svgs-latest/icons.svg
https://gitlab.com/-/trial_registrations/new?glm_source=docs.gitlab.com&amp;glm_content=navigation-cta-docs
https://about.gitlab.com/whats-new/
#!/bin/sh
# assetcrawler 测试请求（凭证为占位符，执行前替换）

# body 已按 key 填充测试值
curl -X POST 'https://gitlab.example.com/api/v4/projects' -H 'Authorization: Bearer ${GITLAB_TOKEN}' -H 'Content-Type: application/json' -d '{"description":"sample-1","name":"user1"}'  # 跳过: POST 非幂等方法，需 AllowUnsafe 才发送

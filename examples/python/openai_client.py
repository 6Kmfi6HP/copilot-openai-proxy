from openai import OpenAI

client = OpenAI(
    api_key="sk-change-me",
    base_url="http://127.0.0.1:8080/v1",
)

response = client.chat.completions.create(
    model="balanced",
    messages=[
        {"role": "system", "content": "You are a concise assistant."},
        {"role": "user", "content": "List two safe rollout checks."},
    ],
)

print(response.choices[0].message.content)

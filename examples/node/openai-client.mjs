import OpenAI from "openai";

const client = new OpenAI({
  apiKey: "sk-change-me",
  baseURL: "http://127.0.0.1:8080/v1",
});

const completion = await client.chat.completions.create({
  model: "creative",
  messages: [
    { role: "system", content: "You are a concise assistant." },
    { role: "user", content: "Give me a short release announcement." },
  ],
});

console.log(completion.choices[0].message.content);

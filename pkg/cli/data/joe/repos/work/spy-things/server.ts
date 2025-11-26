import { serve } from "https://deno.land/std@0.208.0/http/server.ts";

// deno run --allow-net server.ts

const handler = (req: Request): Response => {
  return new Response(
    `<!DOCTYPE html>
<html>
<head>
  <title>Hello World</title>
</head>
<body>
  <h1>Spy Shit</h1>
  <p>Super cool spy shit!</p>
</body>
</html>`,
    {
      status: 200,
      headers: { "content-type": "text/html; charset=utf-8" },
    },
  );
};

serve(handler, { port: 8000 });
console.log("Server running at http://localhost:8000");

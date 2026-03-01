export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    const key = "download_count";

    // 处理计数请求 (仅增加计数，不负责下载重定向)
    if (url.pathname === "/count") {
      if (env.COUNTER) {
        let count = await env.COUNTER.get(key);
        count = parseInt(count) || 0;
        await env.COUNTER.put(key, count + 1);
        return new Response("Counted", { status: 200 });
      } else {
        // 如果没有绑定 KV，静默失败，不影响前端
        return new Response("KV not bound", { status: 200 });
      }
    }

    // 处理获取统计请求
    if (url.pathname === "/stats") {
      // 获取计数
      if (env.COUNTER) {
        const count = await env.COUNTER.get(key);
        return new Response(count || "0", {
          headers: { 
            "Content-Type": "text/plain",
            "Access-Control-Allow-Origin": "*"
          }
        });
      } else {
        return new Response("Error: KV namespace 'COUNTER' not bound. Please check Cloudflare settings.", { status: 500 });
      }
    }

    // 默认行为：返回静态资源 (index.html, appicon.png 等)
    return env.ASSETS.fetch(request);
  }
};

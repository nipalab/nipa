using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Threading;
using Nipa.Editor.Core.Daemon;

namespace Nipa.Core.Tests
{
    public sealed class Reply
    {
        public int Status = 200;
        public string Body = "{}";
        public string[] Lines;
        public bool Hold;
        public int DelayMs;
    }

    public sealed class Req
    {
        public string Method;
        public string Body;
        public string Token;
    }

    public sealed class FakeDaemon : IDisposable
    {
        readonly HttpListener listener = new HttpListener();
        readonly ManualResetEventSlim release = new ManualResetEventSlim();
        public int Port { get; }
        public ConcurrentQueue<Req> Requests { get; } = new ConcurrentQueue<Req>();
        public ConcurrentDictionary<string, Func<Req, Reply>> Handlers { get; } = new ConcurrentDictionary<string, Func<Req, Reply>>();

        public FakeDaemon()
        {
            var tcp = new TcpListener(IPAddress.Loopback, 0);
            tcp.Start();
            Port = ((IPEndPoint)tcp.LocalEndpoint).Port;
            tcp.Stop();
            listener.Prefixes.Add("http://127.0.0.1:" + Port + "/");
            listener.Start();
            new Thread(Loop) { IsBackground = true }.Start();
        }

        public Endpoint Endpoint(string token = "tok") => new Endpoint { Pid = Environment.ProcessId, Port = 1, JsonPort = Port, Token = token, Version = "1.0.0" };

        public void On(string method, Reply reply) => Handlers[method] = _ => reply;

        public void On(string method, string json) => Handlers[method] = _ => new Reply { Body = json };

        void Loop()
        {
            while (listener.IsListening)
            {
                HttpListenerContext ctx;
                try
                {
                    ctx = listener.GetContext();
                }
                catch (Exception)
                {
                    return;
                }
                ThreadPool.QueueUserWorkItem(_ => Handle(ctx));
            }
        }

        void Handle(HttpListenerContext ctx)
        {
            try
            {
                var method = ctx.Request.Url.AbsolutePath.Substring("/v1/".Length);
                string body;
                using (var r = new StreamReader(ctx.Request.InputStream, Encoding.UTF8))
                    body = r.ReadToEnd();
                var req = new Req { Method = method, Body = body, Token = ctx.Request.Headers["x-nipa-daemon-token"] };
                Requests.Enqueue(req);
                if (!Handlers.TryGetValue(method, out var h))
                {
                    ctx.Response.StatusCode = 404;
                    ctx.Response.Close();
                    return;
                }
                var reply = h(req);
                if (reply.DelayMs > 0)
                    Thread.Sleep(reply.DelayMs);
                ctx.Response.StatusCode = reply.Status;
                if (reply.Lines != null)
                {
                    ctx.Response.ContentType = "application/x-ndjson";
                    ctx.Response.SendChunked = true;
                    var w = new StreamWriter(ctx.Response.OutputStream, new UTF8Encoding(false)) { AutoFlush = true };
                    foreach (var line in reply.Lines)
                        w.Write(line + "\n");
                    if (reply.Hold)
                        release.Wait(5000);
                    ctx.Response.Close();
                    return;
                }
                var bytes = Encoding.UTF8.GetBytes(reply.Body);
                ctx.Response.ContentType = "application/json";
                ctx.Response.ContentLength64 = bytes.Length;
                ctx.Response.OutputStream.Write(bytes, 0, bytes.Length);
                ctx.Response.Close();
            }
            catch (Exception)
            {
            }
        }

        public void Dispose()
        {
            release.Set();
            listener.Close();
        }
    }
}

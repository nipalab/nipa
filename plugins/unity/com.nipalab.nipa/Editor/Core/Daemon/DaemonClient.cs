using System;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Runtime.CompilerServices;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Newtonsoft.Json;
using Newtonsoft.Json.Serialization;

namespace Nipa.Editor.Core.Daemon
{
    public sealed class DaemonClient : IDisposable
    {
        public const string TokenHeader = "x-nipa-daemon-token";

        public static readonly JsonSerializerSettings JsonSettings = new JsonSerializerSettings
        {
            ContractResolver = new DefaultContractResolver { NamingStrategy = new SnakeCaseNamingStrategy() },
            NullValueHandling = NullValueHandling.Ignore,
            MissingMemberHandling = MissingMemberHandling.Ignore,
        };

        readonly HttpClient http;
        readonly string baseUrl;
        readonly string token;

        public Endpoint Endpoint { get; }
        public TimeSpan UnaryTimeout { get; set; } = TimeSpan.FromSeconds(30);

        public DaemonClient(Endpoint endpoint, HttpMessageHandler handler = null)
        {
            if (endpoint == null || endpoint.JsonPort <= 0)
                throw new ArgumentException("daemon does not publish a json_port; update the nipa CLI");
            Endpoint = endpoint;
            token = endpoint.Token ?? string.Empty;
            baseUrl = "http://127.0.0.1:" + endpoint.JsonPort + "/v1/";
            http = handler == null ? new HttpClient() : new HttpClient(handler);
            http.Timeout = Timeout.InfiniteTimeSpan;
        }

        public async Task<TRes> CallAsync<TReq, TRes>(string method, TReq request, CancellationToken ct = default)
        {
            using (var cts = CancellationTokenSource.CreateLinkedTokenSource(ct))
            {
                cts.CancelAfter(UnaryTimeout);
                try
                {
                    using (var msg = Build(method, request))
                    using (var resp = await http.SendAsync(msg, cts.Token).ConfigureAwait(false))
                    {
                        var body = await resp.Content.ReadAsStringAsync().ConfigureAwait(false);
                        if (!resp.IsSuccessStatusCode)
                            throw ToException(resp.StatusCode, body);
                        return JsonConvert.DeserializeObject<TRes>(body, JsonSettings);
                    }
                }
                catch (OperationCanceledException) when (!ct.IsCancellationRequested)
                {
                    throw new DaemonException(504, method + " timed out");
                }
            }
        }

        public async IAsyncEnumerable<T> StreamAsync<TReq, T>(
            string method, TReq request, [EnumeratorCancellation] CancellationToken ct = default)
        {
            using (var msg = Build(method, request))
            using (var resp = await http.SendAsync(msg, HttpCompletionOption.ResponseHeadersRead, ct).ConfigureAwait(false))
            {
                if (!resp.IsSuccessStatusCode)
                    throw ToException(resp.StatusCode, await resp.Content.ReadAsStringAsync().ConfigureAwait(false));
                using (var stream = await resp.Content.ReadAsStreamAsync().ConfigureAwait(false))
                using (var reader = new StreamReader(stream, Encoding.UTF8))
                using (ct.Register(() => resp.Dispose()))
                {
                    while (true)
                    {
                        string line;
                        try
                        {
                            line = await reader.ReadLineAsync().ConfigureAwait(false);
                        }
                        catch (Exception) when (ct.IsCancellationRequested)
                        {
                            throw new OperationCanceledException(ct);
                        }
                        if (line == null)
                            yield break;
                        if (line.Length == 0)
                            continue;
                        ct.ThrowIfCancellationRequested();
                        var failure = StreamFailure(line);
                        if (failure != null)
                            throw failure;
                        yield return JsonConvert.DeserializeObject<T>(line, JsonSettings);
                    }
                }
            }
        }

        HttpRequestMessage Build<TReq>(string method, TReq request)
        {
            var msg = new HttpRequestMessage(HttpMethod.Post, baseUrl + method)
            {
                Content = new StringContent(
                    JsonConvert.SerializeObject(request, JsonSettings), Encoding.UTF8, "application/json"),
            };
            msg.Headers.TryAddWithoutValidation(TokenHeader, token);
            return msg;
        }

        static DaemonException StreamFailure(string line)
        {
            if (!line.StartsWith("{\"error\"", StringComparison.Ordinal))
                return null;
            try
            {
                var env = JsonConvert.DeserializeObject<StreamErrorEnvelope>(line, JsonSettings);
                if (env?.Error == null)
                    return null;
                return new DaemonException(StatusFor(env.Error.Code), env.Error.Message ?? env.Error.Code ?? "stream failed");
            }
            catch (JsonException)
            {
                return null;
            }
        }

        static int StatusFor(string code)
        {
            switch (code)
            {
                case "InvalidArgument":
                case "OutOfRange":
                    return 400;
                case "Unauthenticated":
                    return 401;
                case "PermissionDenied":
                    return 403;
                case "NotFound":
                    return 404;
                case "AlreadyExists":
                case "Aborted":
                case "FailedPrecondition":
                    return 409;
                case "ResourceExhausted":
                    return 429;
                case "Canceled":
                    return 499;
                case "Unimplemented":
                    return 501;
                case "Unavailable":
                    return 503;
                case "DeadlineExceeded":
                    return 504;
                default:
                    return 500;
            }
        }

        static DaemonException ToException(HttpStatusCode status, string body)
        {
            try
            {
                var err = JsonConvert.DeserializeObject<ErrorBody>(body, JsonSettings);
                if (err != null && !string.IsNullOrEmpty(err.Message))
                    return new DaemonException((int)status, err.Message);
            }
            catch (JsonException)
            {
            }
            return new DaemonException((int)status, string.IsNullOrWhiteSpace(body) ? status.ToString() : body.Trim());
        }

        public void Dispose()
        {
            http.Dispose();
        }

        sealed class StreamErrorEnvelope
        {
            public ErrorBody Error { get; set; }
        }

        sealed class ErrorBody
        {
            public string Code { get; set; }
            public string Message { get; set; }
        }
    }
}

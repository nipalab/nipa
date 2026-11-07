using System;
using System.Threading;
using System.Threading.Tasks;

namespace Nipa.Editor.Core.Daemon
{
    public static class Ops
    {
        public static async Task<OpResult> RunAsync<TReq>(
            DaemonClient client, string method, TReq request, Action<OpEvent> onEvent, CancellationToken ct)
        {
            OpResult result = null;
            await foreach (var ev in client.StreamAsync<TReq, OpEvent>(method, request, ct).ConfigureAwait(false))
            {
                onEvent?.Invoke(ev);
                if (ev.Failure != null)
                    throw new DaemonException(ev.Failure.Code, ev.Failure.Message);
                if (ev.Result != null)
                    result = ev.Result;
            }
            if (result == null)
                throw new DaemonException(500, method + " ended without a result");
            return result;
        }
    }
}

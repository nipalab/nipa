using System;

namespace Nipa.Editor.Core.Daemon
{
    public sealed class DaemonException : Exception
    {
        public int Code { get; }

        public DaemonException(int code, string message) : base(message)
        {
            Code = code;
        }
    }
}

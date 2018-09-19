using Pbx;
using System;
using System.Collections.Generic;
using System.Text;

namespace Tinode.ChatBot
{
    /// <summary>
    /// ChatBot reply interface, you can define your own chat reply logic.
    /// </summary>
    public interface IBotResponse
    {
        /// <summary>
        /// ChatBot Accept Message, and give a resonable reply.
        /// </summary>
        /// <param name="message">message to chatbot</param>
        /// <returns>message reply by chatbot</returns>
        string ThinkAndReply(ServerData message);
    }
}

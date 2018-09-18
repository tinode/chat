using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Tinode.ChatBot;

namespace Tinode.ChatBot.DemoNet46
{
    class Program
    {
        static ChatBot bot;
        static CancellationTokenSource ctx;
        static void Main(string[] args)
        {
            Console.CancelKeyPress += Console_CancelKeyPress;
            bot = new ChatBot();
            ctx = new CancellationTokenSource();
            bot.Start();
            while (true)
            {
                if (ctx.IsCancellationRequested)
                {
                    break;
                }
                Thread.Sleep(1000);
            }
            Console.WriteLine("ChatBot Stopped");
        }

        private static  void Console_CancelKeyPress(object sender, ConsoleCancelEventArgs e)
        {
            bot.Stop();
            ctx.Cancel();
            
        }
    }
}

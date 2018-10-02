using CommandLine;
using Google.Protobuf;
using Newtonsoft.Json;
using Pbx;
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
        public class CmdOptions
        {
            [Option('C', "login-cookie", Required = false, Default = ".tn-cookie", HelpText = "read credentials from the provided cookie file")]
            public string CookieFile { get; set; }
            [Option('T', "login-token", Required = false, HelpText = "login using token authentication")]
            public string Token { get; set; }
            [Option('B', "login-basic", Required = false, HelpText = "login using basic authentication username:password")]
            public string Basic { get; set; }
            [Option('L', "listen", Required = false, Default = "0.0.0.0:40052", HelpText = "address to listen on for incoming Plugin API calls")]
            public string Listen { get; set; }
            [Option('S', "server", Required = false, Default = "localhost:6061", HelpText = "address of Tinode server gRPC endpoint")]
            public string Host { get; set; }
        }

        /// <summary>
        /// ChatBot auto reply implement
        /// </summary>
        public class BotReponse : IBotResponse
        {
            public string ThinkAndReply(ServerData message)
            {
                var ret = string.Empty;
                foreach (var sub in bot.Subscribers)
                {
                    ret += $"{sub.Value.UserName}\r\n";
                }
                return ret;
            }

        }

        static ChatBot bot;
        static void Main(string[] args)
        {
            Console.CancelKeyPress += Console_CancelKeyPress;
            string schemaArg = string.Empty;
            string secretArg = string.Empty;
            string cookieFile = string.Empty;
            string host = string.Empty;
            string listen = string.Empty;
            Parser.Default.ParseArguments<CmdOptions>(args)
                   .WithParsed<CmdOptions>(o =>
                   {
                       if (!string.IsNullOrEmpty(o.Host))
                       {
                           host = o.Host;
                           Console.WriteLine($"gRPC server:{host}");
                       }
                       if (!string.IsNullOrEmpty(o.Listen))
                       {
                           listen = o.Listen;
                           Console.WriteLine($"Plugin API calls Listen server:{listen}");
                       }
                       if (!string.IsNullOrEmpty(o.Token))
                       {
                           schemaArg = "token";
                           secretArg = Encoding.ASCII.GetString(Encoding.Default.GetBytes(o.Token));
                           Console.WriteLine($"Login in with token {o.Token}");
                           bot = new ChatBot(serverHost: host, listen: listen, schema: schemaArg, secret: secretArg);
                       }
                       else if (!string.IsNullOrEmpty(o.Basic))
                       {
                           schemaArg = "basic";
                           secretArg = Encoding.UTF8.GetString(Encoding.Default.GetBytes(o.Basic));
                           Console.WriteLine($"Login in with login:password {o.Basic}");
                           bot = new ChatBot(serverHost: host, listen: listen, schema: schemaArg, secret: secretArg);
                       }
                       else
                       {
                           cookieFile = o.CookieFile;
                           Console.WriteLine($"Login in with cookie file {o.CookieFile}");
                           bot = new ChatBot(serverHost: host, listen: listen, cookie: cookieFile, schema: string.Empty, secret: string.Empty);
                           if (bot.ReadAuthCookie(out var schem, out var secret))
                           {
                               bot.Schema = schem;
                               bot.Secret = secret;
                           }
                           else
                           {
                               Console.WriteLine("Login in with cookie file failed, please check your credentials and try again... Press any key to exit.");
                               Console.ReadKey();
                               return;
                           }
                       }
                       bot.BotResponse = new BotReponse();
                       bot.Start().Wait();

                       Console.WriteLine("[Bye Bye] ChatBot Stopped");
                   });

        }

        private static void Console_CancelKeyPress(object sender, ConsoleCancelEventArgs e)
        {
            bot.Stop();
        }
    }

}

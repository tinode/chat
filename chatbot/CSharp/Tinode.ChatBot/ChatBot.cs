using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using System.Threading.Tasks;
using Pbx;
using Grpc.Core;
using Grpc;
using static Pbx.Plugin;
using static Pbx.Node;
using Google.Protobuf;
using System.Threading;
using System.Collections.Concurrent;
using Newtonsoft.Json;
using Google.Protobuf.Collections;
using System.Runtime.InteropServices;
using System.IO;

namespace Tinode.ChatBot
{
    public class ChatBotPlugin : PluginBase
    {
        public override Task<Unused> Account(AccountEvent request, ServerCallContext context)
        {
            string action = string.Empty;
            if (request.Action==Crud.Create)
            {
                action = "created";
            }
            else if (request.Action==Crud.Update)
            {
                action = "updated";
            }
            else if (request.Action==Crud.Delete)
            {
                action = "deleted";
            }
            else
            {
                action = "unknown";
            }
            return Task.FromResult(new Unused());
        }
    }


    public class ChatBot
    {
        public class Future
        {
            public string Tid { get; private set; }
            public string Arg { get; private set; }
            public Action<string, MapField<string, ByteString>> Action { get; private set; }

            public Future(string tid,Action<string, MapField<string, ByteString>> action,string arg="")
            {
                Tid = tid;
                Action = action;
                Arg = arg;
            }
        }

        public string AppName => "CBot";
        public string AppVersion => "0.15.5";
        public string LibVersion => "0.15.5";
        public string Platform => $"({RuntimeInformation.OSDescription} {RuntimeInformation.OSArchitecture})";
        public string CookieFile => "cookie.dat";
        public string BotUID { get; private set; }
        public long NextTid { get; private set; }

        public string ServerHost { get; set; }
        public int ListenPort { get; set; }
        Server server;
        AsyncDuplexStreamingCall<ClientMsg, ServerMsg> client;
        Channel channel;
        CancellationTokenSource cancellationTokenSource;
        Queue<ClientMsg> sendMsgQueue;
        Dictionary<string, bool> subscriptions;
        Dictionary<string, Future> onCompletion;
        public ChatBot(string serverHost="localhost:6061",int listenPort=40052)
        {
            NextTid =new Random().Next(1,1000);
            ServerHost = serverHost;
            ListenPort = listenPort;
            cancellationTokenSource = new CancellationTokenSource();
            sendMsgQueue = new Queue<ClientMsg>();
            subscriptions = new Dictionary<string, bool>();
            onCompletion = new Dictionary<string, Future>();
        }

        public string GetNextTid()
        {
            NextTid += 1;
            return NextTid.ToString();
        }
        
        
        public Server InitServer()
        {
            var server = new Server();
            server.Services.Add(Plugin.BindService(new ChatBotPlugin()));
            server.Ports.Add(new ServerPort("0.0.0.0", ListenPort, ServerCredentials.Insecure));
            server.Start();
            return server;
        }

        public AsyncDuplexStreamingCall<ClientMsg, ServerMsg> InitClient()
        {
            channel = new Channel(ServerHost, ChannelCredentials.Insecure);
            var stub = new NodeClient(channel);
            var stream = stub.MessageLoop(cancellationToken:cancellationTokenSource.Token);
            return stream;
        }

        public void AddFuture(string tid,Future bundle)
        {
            onCompletion.Add(tid, bundle);
        }

        public void ExecFuture(string tid,int code,string text,MapField<string,ByteString> paramaters)
        {
            if (onCompletion.ContainsKey(tid))
            {
                var bundle = onCompletion[tid];
                onCompletion.Remove(tid);
                if (code>=200 && code<400)
                {
                    var arg = bundle.Arg;
                    bundle.Action(arg, paramaters);
                }
                else
                {
                    Console.WriteLine($"Error:{code}, {text}");
                }
            }

        }

        public void AddSubscription(string topic)
        {
            if (!subscriptions.ContainsKey(topic))
            {
                subscriptions.Add(topic, true);
            }
        }

        public void DelSubscription(string topic)
        {
            if (subscriptions.ContainsKey(topic))
            {
                subscriptions.Remove(topic);
            }
        }

        public void ServerVersion(MapField<string, ByteString> paramaters)
        {
            if (paramaters==null)
            {
                return;
            }
            Console.WriteLine($"Server:{paramaters["build"].ToString(Encoding.ASCII)},{paramaters["ver"].ToString(Encoding.ASCII)}");
        }

        public void OnLogin(string cookieFile, MapField<string, ByteString> paramaters)
        {
            if (paramaters == null || string.IsNullOrEmpty(cookieFile))
            {
                return;
            }
            if (paramaters.ContainsKey("user"))
            {
                BotUID = paramaters["user"].ToString(Encoding.ASCII);
            }
            Dictionary<string, string> cookieDics = new Dictionary<string, string>();
            cookieDics["schema"] = "token";
            if (paramaters.ContainsKey("token"))
            {
                cookieDics["secret"] = JsonConvert.DeserializeObject<string>(paramaters["token"].ToString(Encoding.UTF8));
                cookieDics["expires"] = JsonConvert.DeserializeObject<string>(paramaters["expires"].ToString(Encoding.UTF8));
            }
            else
            {
                cookieDics["schema"] = "basic";
                cookieDics["secret"] = JsonConvert.DeserializeObject<string>(paramaters["token"].ToString(Encoding.UTF8));
            }
            try
            {
                using (FileStream stream = new FileStream(cookieFile, FileMode.Create,FileAccess.Write))
                using (StreamWriter w = new StreamWriter(stream))
                {
                    w.Write(JsonConvert.SerializeObject(cookieDics));
                }

            }
            catch (Exception e)
            {
                Console.WriteLine($"Failed to save authentication cookie:{e}");
            }

        }

        public bool ReadAuthCookie(out string schema,out string secret)
        {
            schema = null;
            secret =null;
            if (!File.Exists(CookieFile))
            {
                return false;
            }

            try
            {
                using (FileStream stream = new FileStream(CookieFile, FileMode.Open,FileAccess.Read))
                using (StreamReader r = new StreamReader(stream))
                {
                    var cookies = JsonConvert.DeserializeObject<Dictionary<string, string>>(r.ReadToEnd());
                    schema = cookies["schema"];
                    secret = null;
                    if (schema == "token")
                    {
                        secret = Encoding.UTF8.GetString(Convert.FromBase64String(cookies["secret"]));
                    }
                    else
                    {
                        secret = cookies["secret"];
                    }
                    return true;

                }
            }
            catch (Exception)
            {
                return false;
            }
            
        }

        public void ClientPost(ClientMsg msg)
        {
            sendMsgQueue.Enqueue(msg);
        }

        public void ClientReset()
        {
            try
            {
                while (sendMsgQueue.Count > 0)
                {
                    sendMsgQueue.Dequeue();
                }
            }
            catch (Exception e)
            {
            }
        }

        public ClientMsg Hello()
        {
            var tid = GetNextTid();
            AddFuture(tid, new Future(tid, new Action<string, MapField<string, ByteString>>((unused, paramaters) =>
            {
                ServerVersion(paramaters);
            })));

            return new ClientMsg() { Hi = new ClientHi() { Id = tid, UserAgent = $"{AppName}/{AppVersion} {Platform}; gRPC-csharp/{AppVersion}", Ver = LibVersion, Lang = "EN" } };
        }

        public ClientMsg Login(string cookieFile,string scheme,string secret)
        {
            var tid = GetNextTid();
            AddFuture(tid, new Future(tid, new Action<string, MapField<string, ByteString>>((fname, paramaters) =>
            {
                OnLogin(fname, paramaters);
            }),cookieFile));

            return new ClientMsg() { Login = new ClientLogin() { Id = tid, Scheme = scheme, Secret = ByteString.CopyFrom(secret, Encoding.UTF8) } };
        }

        public ClientMsg Subscribe(string topic)
        {
            var tid = GetNextTid();
            AddFuture(tid, new Future(tid, new Action<string, MapField<string, ByteString>>((topicName, unused) =>
            {
                AddSubscription(topicName);
            }),topic));

            return new ClientMsg() { Sub = new ClientSub() { Id = tid, Topic = topic } };
        }

        public ClientMsg Leave(string topic)
        {
            var tid = GetNextTid();
            AddFuture(tid, new Future(tid, new Action<string, MapField<string, ByteString>>((topicName, unused) =>
            {
                DelSubscription(topicName);
            }), topic));

            return new ClientMsg() { Leave = new ClientLeave() { Id = tid, Topic = topic } };
        }

        public ClientMsg Publish(string topic,string text)
        {
            var tid = GetNextTid();
            var message = ByteString.CopyFromUtf8(JsonConvert.SerializeObject(text));

            return new ClientMsg() { Pub = new ClientPub() { Id = tid, Topic = topic, NoEcho = true, Content = message } };
        }

        public ClientMsg NoteRead(string topic, int seq)
        {
            return new ClientMsg() { Note = new ClientNote() { SeqId = seq, Topic = topic, What = InfoNote.Read } };
        }

        public async Task Start()
        {
            server= InitServer();
            client = InitClient();
            Task sendBackendTask = new Task(async () =>
            {
                Console.WriteLine("Message send queue started...");
                while (!cancellationTokenSource.IsCancellationRequested)
                {
                    if (sendMsgQueue.Count > 0)
                    {
                        var msg = sendMsgQueue.Dequeue();
                        await client.RequestStream.WriteAsync(msg);

                    }
                    else
                    {
                        Thread.Sleep(10);
                    }
                }
                Console.WriteLine("Detect cancel message,stop sending message...");
            },cancellationTokenSource.Token);
            sendBackendTask.Start();
            ClientPost(Hello());
            //Thread.Sleep(50);
            ClientPost(Login(CookieFile, "basic", "bob:bob123"));
            //Thread.Sleep(50);
            ClientPost(Subscribe("me"));
            //Thread.Sleep(50);
            
            try
            {
                while (!cancellationTokenSource.IsCancellationRequested&& await client.ResponseStream.MoveNext())
                {
                    var response = client.ResponseStream.Current;
                    if (response.Ctrl != null)
                    {
                        Console.WriteLine($"Ctrl:{response.Ctrl.Id},{response.Ctrl.Code},{response.Ctrl.Text},{response.Ctrl.Params}");
                        ExecFuture(response.Ctrl.Id, response.Ctrl.Code, response.Ctrl.Text, response.Ctrl.Params);
                    }
                    else if (response.Data != null)
                    {
                        if (response.Data.FromUserId != BotUID)
                        {
                            ClientPost(NoteRead(response.Data.Topic, response.Data.SeqId));
                            Thread.Sleep(50);
                            ClientPost(Publish(response.Data.Topic, "你好大兄弟"));
                        }
                    }
                    else if (response.Pres != null)
                    {
                        if (response.Pres.Topic == "me")
                        {
                            Console.WriteLine("me");
                            if ((response.Pres.What == ServerPres.Types.What.On || response.Pres.What == ServerPres.Types.What.Msg)&&!subscriptions.ContainsKey(response.Pres.Src))
                            {
                                ClientPost(Subscribe(response.Pres.Src));
                            }
                            else if (response.Pres.What==ServerPres.Types.What.Off && subscriptions.ContainsKey(response.Pres.Src))
                            {
                                ClientPost(Leave(response.Pres.Src));
                            }
                        }
                    }
                }
            }
            catch (Exception e)
            {
                Console.WriteLine($"Connection Closed:{e}");
                Thread.Sleep(3000);
                ClientReset();
                client = InitClient();
            }
        } 
        
        public void Stop()
        {
            server.KillAsync().Wait();
            channel.ShutdownAsync().Wait();
            cancellationTokenSource.Cancel();
        }

    }
}
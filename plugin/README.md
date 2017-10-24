# Plugins / External Services

Tinode server supports interaction with external services referred to as plugins in this document.

Plugins are generally executed as separate processes. Tinode server communicates with them using [gRPC](https://grpc.io/) as defined in model.proto file. Plugins can be written in any of supported languages (as of the time of this writing it's C++, C#, Go, Java, Node, PHP, Python, Ruby).

Plugins have to define three methods:

 * `HandleMessage(ClientReq) returns (ServerCtrl)` <br>
This method is called for every message received from the client. ClientReq contains serialized client message and sessions data. The method returns a ServerCtrl message. If ServerCtrl.Code is 0, the server will continue with the default processing of the message. A non-zero ServerCtrl.Code indicates that no further processing is needed. Server will generate a {ctrl} message from the ServerCtrl and forward it to the client session. 	

 * `FilterMessage(ServerMsg) returns (ServerMsg)` <br>
This method is called immmediately before a server message is broadcasted to topic subscribers. The filter may alter the server message or may request to drop it.

 * `RequestMessages(None) returns (stream ClientMsg)` <br>
The method returns a stream of messages which are treated as if coming from a client.

 

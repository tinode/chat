%% Support module for Tinode load testing with Tsung.
%% Compile using erlc then copy resulting .beam to
%% /usr/local/lib/erlang/lib/tsung-1.7.0/ebin/
%% Alternatively you can just leave it in the current
%% directory.

-module(tinode).
-export([rand_user_secret/1, shuffle/1, cache_token/2, read_token/1]).

%% Produces a secret for use in basic login.
rand_user_secret({Pid, DynData}) ->
  base64:encode_to_string(get_rand_secret()).


%% Unexported. Picks a random user from a pre-defined list.
get_rand_secret() ->
  case rand:uniform(6) of
      1 -> "alice:alice123";
      2 -> "bob:bob123";
      3 -> "carol:carol123";
      4 -> "dave:dave123";
      5 -> "eve:eve123";
      6 -> "frank:frank123"
  end.

%% Shuffles a list randomly.
shuffle(L) ->
  RandomList=[{rand:uniform(), X} || X <- L],
  [X || {_,X} <- lists:sort(RandomList)].

%% Reads previously cached token for the specified user.
read_token(Uid) ->
  {ok, LogDir} = application:get_env(tsung_controller, log_dir_real),
  case file:read_file(filename:join(LogDir, Uid)) of
    {ok, Data} -> string:trim(Data);
    {error, _} -> ""
  end.

%% Saves auth token for the specified user in the log directory.
cache_token(Uid, Token) ->
  {ok, LogDir} = application:get_env(tsung_controller, log_dir_real),
  {ok, File} = file:open(filename:join(LogDir, Uid), [write]),
  file:write(File, Token),
  file:close(File),
  ok.

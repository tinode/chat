package tinode

import java.util.Base64
import java.util.concurrent.ConcurrentHashMap

import scala.collection.JavaConverters._
import scala.collection._
import scala.concurrent.duration._

import io.gatling.core.Predef._
import io.gatling.http.Predef._

class Loadtest extends Simulation {
  val httpProtocol = http
    .baseUrl("http://localhost:6060")
    .wsBaseUrl("ws://localhost:6060")

  val feeder = csv("users.csv").random

  // Auth tokens to share between sessions.
  val tokenCache : concurrent.Map[String, String] = new ConcurrentHashMap() asScala

  val loginBasic =
    exec { session =>
      val uname = session("username").as[String]
      val password = session("password").as[String]
      val secret = new String(java.util.Base64.getEncoder.encode((uname + ":" + password).getBytes()))
      session.set("secret", secret)
    }
    .exec {
      ws("login").sendText(
        """{"login":{"id":"${id}-login","scheme":"basic","secret":"${secret}"}}"""
      )
      .await(5 seconds)(
        ws.checkTextMessage("login-ctrl")
          .matching(jsonPath("$.ctrl").find.exists)
          .check(jsonPath("$.ctrl.params.token").saveAs("token"))
      )
    }
    .exec { session =>
      val uname = session("username").as[String]
      val token = session("token").as[String]
      tokenCache.put(uname, token)
      session
    }

  val loginToken =
    exec { session =>
      val uname = session("username").as[String]
      val token = tokenCache.get(uname).getOrElse("")
      session.set("token", token)
    }
    .exec {
      ws("login-token").sendText(
        """{"login":{"id":"${id}-login2","scheme":"token","secret":"${token}"}}"""
      )
      .await(5 seconds)(
        ws.checkTextMessage("login-ctrl")
          .matching(jsonPath("$.ctrl").find.exists)
      )
    }

  val scn = scenario("WebSocket")
    .exec(ws("Connect WS").connect("/v0/channels?apikey=AQEAAAABAAD_rAp4DJh05a1HAwFT3A6K"))
    .exec(session => session.set("id", "tn-" + session.userId))
    .pause(1)
    .exec {
      ws("hi").sendText(
        """{"hi":{"id":"afabb3","ver":"0.16","ua":"Gatling-Loadtest/1.0; gatling/1.7.0"}}"""
      )
    }
    .pause(1)
    .feed(feeder)
    .doIfOrElse({session =>
      val uname = session("username").as[String]
      val token = tokenCache.get(uname)
      token == None
    }) { loginBasic } { loginToken }
    .exec {
      ws("sub-me").sendText(
        """{"sub":{"id":"{id}-sub-me","topic":"me","get":{"what":"desc"}}}"""
      )
      .await(5 seconds)(
        ws.checkTextMessage("sub-me-desc")
          .matching(jsonPath("$.meta.desc").find.exists)
      )
    }
    .exec {
      ws("get-subs").sendText(
        """{"get":{"id":"${id}-get-subs","topic":"me","what":"sub"}}"""
      )
      .await(5 seconds)(
        ws.checkTextMessage("save-subs")
          .matching(jsonPath("$.meta.sub").find.exists)
          .check(jsonPath("$.meta.sub[*].topic").findAll.saveAs("subs"))
      )
    }
    .foreach("${subs}", "sub") {
      exec {
        ws("sub-topic").sendText(
          """{"sub":{"id":"${id}-sub-${sub}","topic":"${sub}","get":{"what":"desc sub data del"}}}"""
        )
        .await(5 seconds)(
          ws.checkTextMessage("sub-topic-ctrl")
            .matching(jsonPath("$.ctrl").find.exists)
        )
      }
      .repeat(3, "i") {
        exec {
          ws("pub-topic").sendText(
            """{"pub":{"id":"${id}-pub-${sub}-${i}","topic":"${sub}","content":"This is a Tsung test ${i}"}}"""
          )
          .await(5 seconds)(
            ws.checkTextMessage("pub-topic-ctrl")
              .matching(jsonPath("$.ctrl").find.exists)
          )
        }
      }
      .exec {
        ws("leave-topic").sendText(
          """{"leave":{"id":"${id}-leave-${sub}","topic":"${sub}"}}"""
        )
        .await(5 seconds)(
          ws.checkTextMessage("sub-topic-ctrl")
            .matching(jsonPath("$.ctrl").find.exists)
        )
      }
    }
    .exec(ws("close-ws").close)

  setUp(scn.inject(rampUsers(5000) during (50 seconds))).protocols(httpProtocol)
}

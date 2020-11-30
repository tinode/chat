package tinode

import scala.concurrent.duration._

import io.gatling.core.Predef._
import io.gatling.http.Predef._

class WS2 extends Simulation {
  val httpProtocol = http
    .baseUrl("http://localhost:6060")
    .wsBaseUrl("ws://localhost:6060")

  val feeder = csv("users.csv").random

  val scn = scenario("WebSocket")
    .exec(ws("Connect WS").connect("/v0/channels?apikey=AQEAAAABAAD_rAp4DJh05a1HAwFT3A6K"))
    .exec(session => session.set("id", "Tinode_" + session.userId))
    .pause(1)
    .repeat(1, "i") {
      exec {
        ws("hi").sendText(
          """{"hi":{"id":"afabb3","ver":"0.16","ua":"Gatling-Loadtest/1.0; gatling/1.7.0"}}"""
        )
      }
    }
    .pause(1)
    .feed(feeder)
    .exec {
      ws("login").sendText(
        """{"login":{"id":"afabb302","scheme":"basic","secret":"${secret}"}}"""
      )
      .await(5 seconds)(
        ws.checkTextMessage("login-ctrl")
          .matching(jsonPath("$.ctrl").find.exists)
      )
    }
    .exec {
      ws("sub-me").sendText(
        """{"sub":{"id":"afbef03","topic":"me","get":{"what":"desc"}}}"""
      )
      .await(5 seconds)(
        ws.checkTextMessage("sub-me-desc")
          .matching(jsonPath("$.meta.desc").find.exists)
      )
    }
    .exec {
      ws("get-subs").sendText(
        """{"get":{"id":"afbef04","topic":"me","what":"sub"}}"""
      )
      .await(5 seconds)(
        ws.checkTextMessage("subs3")
          .matching(jsonPath("$.meta.sub").find.exists)
          .check(jsonPath("$.meta.sub[*].topic").findAll.saveAs("subs"))
      )
    }
    .exec(session => {
      val subs = session("subs").asOption[String]
      println(subs.getOrElse("COULD NOT FIND topics"))
      session
    })
    .foreach("${subs}", "sub") {
      exec {
        ws("sub-topic").sendText(
          """{"sub":{"id":"afn-${sub}","topic":"${sub}","get":{"what":"desc sub data del"}}}"""
        )
        .await(5 seconds)(
          ws.checkTextMessage("sub-topic-ctrl")
            .matching(jsonPath("$.ctrl").find.exists)
        )
      }
      .repeat(3, "i") {
        exec {
          ws("pub-topic").sendText(
            """{"pub":{"id":"pub-${sub}-${i}","topic":"${sub}","content":"This is a Tsung test ${i}"}}"""
          )
          .await(5 seconds)(
            ws.checkTextMessage("pub-topic-ctrl")
              .matching(jsonPath("$.ctrl").find.exists)
          )
        }
      }
      .exec {
        ws("leave-topic").sendText(
          """{"leave":{"id":"leave-${sub}","topic":"${sub}"}}"""
        )
        .await(5 seconds)(
          ws.checkTextMessage("sub-topic-ctrl")
            .matching(jsonPath("$.ctrl").find.exists)
        )
      }
    }
    .exec(ws("close-ws").close)

  setUp(scn.inject(rampUsers(5) during (5 seconds))).protocols(httpProtocol)
}


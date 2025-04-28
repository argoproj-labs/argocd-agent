// Copyright 2024 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/argoproj-labs/argocd-agent/pkg/client"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/stretchr/testify/require"
)

func newAgent(t *testing.T) *Agent {
	t.Helper()
	kubec := kube.NewKubernetesFakeClient()
	remote, err := client.NewRemote("127.0.0.1", 8080)
	require.NoError(t, err)
	agent, err := NewAgent(context.TODO(), kubec, "argocd", WithRemote(remote))
	require.NoError(t, err)
	return agent
}

func Test_NewAgent(t *testing.T) {
	kubec := kube.NewKubernetesFakeClient()
	agent, err := NewAgent(context.TODO(), kubec, "agent", WithRemote(&client.Remote{}))
	require.NotNil(t, agent)
	require.NoError(t, err)
}

// func Test_AgentNewAppFromInformer(t *testing.T) {
// 	agent := newAgent(t)
// 	require.NotNil(t, agent)
// 	err := agent.Start(context.Background())
// 	require.NoError(t, err)

// 	t.Run("Add application event when agent is not connected", func(t *testing.T) {
// 		syncch := make(chan bool)
// 		ocb := agent.informer.NewAppCallback()
// 		ncb := func(app *v1alpha1.Application) {
// 			ocb(app)
// 			syncch <- true
// 		}
// 		agent.informer.SetNewAppCallback(ncb)
// 		defer agent.informer.SetNewAppCallback(ocb)
// 		agent.informer.EnsureSynced(1 * time.Second)
// 		_, err := agent.appclient.ArgoprojV1alpha1().Applications(agent.namespace).Create(agent.context, &v1alpha1.Application{
// 			ObjectMeta: v1.ObjectMeta{
// 				Name:      "testapp1",
// 				Namespace: agent.namespace,
// 			},
// 		}, v1.CreateOptions{})
// 		require.NoError(t, err)
// 		// Wait for the informer callback to finish
// 		<-syncch
// 		assert.Equal(t, 0, agent.queues.SendQ(defaultQueueName).Len())
// 	})

// 	t.Run("Add application event when agent is connected", func(t *testing.T) {
// 		syncch := make(chan bool)
// 		ocb := agent.informer.NewAppCallback()
// 		ncb := func(app *v1alpha1.Application) {
// 			ocb(app)
// 			syncch <- true
// 		}
// 		agent.informer.SetNewAppCallback(ncb)
// 		defer agent.informer.SetNewAppCallback(ocb)
// 		agent.connected.Store(true)
// 		agent.informer.EnsureSynced(1 * time.Second)
// 		_, err := agent.appclient.ArgoprojV1alpha1().Applications(agent.namespace).Create(agent.context, &v1alpha1.Application{
// 			ObjectMeta: v1.ObjectMeta{
// 				Name:      "testapp2",
// 				Namespace: agent.namespace,
// 			},
// 		}, v1.CreateOptions{})
// 		require.NoError(t, err)
// 		// Wait for the informer callback to finish
// 		<-syncch
// 		assert.Equal(t, 1, agent.queues.SendQ(defaultQueueName).Len())
// 		ev, _ := agent.queues.SendQ(defaultQueueName).Get()
// 		require.IsType(t, event.Event{}, ev)
// 		assert.Equal(t, event.EventAppAdded, ev.(event.Event).Type)
// 		assert.Equal(t, "testapp2", ev.(event.Event).Application.Name)
// 	})

// 	time.Sleep(1 * time.Second)
// 	err = agent.Stop()
// 	require.NoError(t, err)
// }

// func Test_AgentUpdateAppFromInformer(t *testing.T) {
// 	app := &v1alpha1.Application{
// 		ObjectMeta: v1.ObjectMeta{
// 			Name:      "testapp",
// 			Namespace: "agent",
// 		},
// 	}
// 	fakec := fakekube.NewFakeClientsetWithResources()
// 	appc := fakeappclient.NewSimpleClientset(app)
// 	agent, err := NewAgent(context.TODO(), fakec, appc, "agent")
// 	require.NotNil(t, agent)
// 	require.NoError(t, err)
// 	err = agent.Start(context.Background())
// 	require.NoError(t, err)

// 	t.Run("Update application event when agent is connected", func(t *testing.T) {
// 		syncch := make(chan bool)
// 		ocb := agent.informer.UpdateAppCallback()
// 		ncb := func(old *v1alpha1.Application, new *v1alpha1.Application) {
// 			ocb(old, new)
// 			syncch <- true
// 		}
// 		agent.informer.SetUpdateAppCallback(ncb)
// 		defer agent.informer.SetUpdateAppCallback(ocb)
// 		agent.connected.Store(true)
// 		agent.informer.EnsureSynced(1 * time.Second)
// 		_, err := appc.ArgoprojV1alpha1().Applications(agent.namespace).Update(agent.context, app, v1.UpdateOptions{})
// 		require.NoError(t, err)
// 		<-syncch
// 		// assert.Equal(t, 1, agent.queues.SendQ(defaultQueueName).Len())
// 		ev, _ := agent.queues.SendQ(defaultQueueName).Get()
// 		require.IsType(t, event.Event{}, ev)
// 		assert.Equal(t, event.EvenAppStatusUpdated, ev.(event.Event).Type)
// 		assert.Equal(t, "testapp", ev.(event.Event).Application.Name)
// 	})

// }

// func Test_AgentDeleteAppFromInformer(t *testing.T) {
// 	t.Run("Delete application event when agent is not connected", func(t *testing.T) {
// 		app := &v1alpha1.Application{
// 			ObjectMeta: v1.ObjectMeta{
// 				Name:      "testapp",
// 				Namespace: "agent",
// 			},
// 		}
// 		fakec := fakekube.NewFakeClientsetWithResources()
// 		appc := fakeappclient.NewSimpleClientset(app)
// 		agent, err := NewAgent(context.TODO(), fakec, appc, "agent")
// 		require.NotNil(t, agent)
// 		require.NoError(t, err)
// 		err = agent.Start(context.Background())
// 		require.NoError(t, err)

// 		syncch := make(chan bool)
// 		ocb := agent.informer.DeleteAppCallback()
// 		ncb := func(app *v1alpha1.Application) {
// 			ocb(app)
// 			syncch <- true
// 		}
// 		agent.informer.SetDeleteAppCallback(ncb)
// 		defer agent.informer.SetDeleteAppCallback(ocb)
// 		agent.informer.EnsureSynced(1 * time.Second)
// 		err = appc.ArgoprojV1alpha1().Applications(agent.namespace).Delete(agent.context, app.Name, v1.DeleteOptions{})
// 		require.NoError(t, err)
// 		// Wait for the informer callback to finish
// 		<-syncch
// 		assert.Equal(t, 0, agent.queues.SendQ(defaultQueueName).Len())
// 	})

// 	t.Run("Delete application event when agent is connected", func(t *testing.T) {
// 		app := &v1alpha1.Application{
// 			ObjectMeta: v1.ObjectMeta{
// 				Name:      "testapp",
// 				Namespace: "agent",
// 			},
// 		}
// 		fakec := fakekube.NewFakeClientsetWithResources()
// 		appc := fakeappclient.NewSimpleClientset(app)
// 		agent, err := NewAgent(context.TODO(), fakec, appc, "agent")
// 		require.NotNil(t, agent)
// 		require.NoError(t, err)
// 		err = agent.Start(context.Background())
// 		require.NoError(t, err)

// 		syncch := make(chan bool)
// 		ocb := agent.informer.DeleteAppCallback()
// 		ncb := func(app *v1alpha1.Application) {
// 			ocb(app)
// 			syncch <- true
// 		}
// 		agent.informer.SetDeleteAppCallback(ncb)
// 		defer agent.informer.SetDeleteAppCallback(ocb)
// 		agent.connected.Store(true)
// 		agent.informer.EnsureSynced(1 * time.Second)
// 		err = appc.ArgoprojV1alpha1().Applications(agent.namespace).Delete(agent.context, app.Name, v1.DeleteOptions{})
// 		require.NoError(t, err)
// 		// Wait for the informer callback to finish
// 		<-syncch
// 		assert.Equal(t, 1, agent.queues.SendQ(defaultQueueName).Len())
// 	})

// }

func Test_Healthz(t *testing.T) {
	agent := newAgent(t)
	require.NotNil(t, agent)

	t.Run("Healthz when agent is connected", func(t *testing.T) {
		agent.SetConnected(true)
		testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			agent.healthzHandler(w, r)
		}))
		defer testServer.Close()

		resp, err := http.Get(testServer.URL + "/healthz")
		require.NoError(t, err)

		require.Equal(t, http.StatusOK, resp.StatusCode)
		defer resp.Body.Close()
	})

	t.Run("Healthz when agent is not connected", func(t *testing.T) {
		agent.SetConnected(false)
		testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			agent.healthzHandler(w, r)
		}))
		defer testServer.Close()

		resp, err := http.Get(testServer.URL + "/healthz")
		require.NoError(t, err)

		require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
		defer resp.Body.Close()
	})

}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}

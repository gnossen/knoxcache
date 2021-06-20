@Library("artifact-store-library") _

pipeline {
    agent { label "common" }

    post {
        always {
            deleteDir()
        }
    }

    options {
        disableConcurrentBuilds()
    }

    stages {
        stage('Build Browser Extensions') {
            // TODO: Only when there are changes to it.
            sh "cd chromium-extension; make"
            sh "cd firefox-addon; make"
        }

        stage('Publish Browser Extensions') {
            // TODO: Only when there are changes to it.
            when { branch "main" }
            environment {
                ART_STORE = credentials("artifact-store")
            }
            steps {
                script {
                    chromiumVersion = sh(script: "cd chromium-extension; make print-version", returnStdout: true).trim()
                    chromiumArtifactName = "knox-chromium-extension-${chromiumVersion}.zip"
                    chromiumRemoteDirectory = "knox/chromium-extension"
                    uploadArtifact("chromium-extension/${chromiumArtifactName}", chromiumRemoteDirectory, env.ART_STORE_USR, env.ART_STORE_PSW)
                    updateLink("knox/chromium-extension", name, "chromium-extension-latest.zip", env.ART_STORE_USR, env.ART_STORE_PSW)

                    // TODO: Firefox.
                }
            }
        }
    }
}
